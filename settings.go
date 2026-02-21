package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/gobwas/glob"
)

// PermissionSettings holds permission rules from a settings file.
type PermissionSettings struct {
	Allow                 []string `json:"allow,omitempty"`
	Deny                  []string `json:"deny,omitempty"`
	Ask                   []string `json:"ask,omitempty"`
	AdditionalDirectories []string `json:"additionalDirectories,omitempty"`
	DefaultMode           string   `json:"defaultMode,omitempty"`
}

// ClaudeCodeSettings represents the structure of a Claude Code settings file.
type ClaudeCodeSettings struct {
	Permissions *PermissionSettings `json:"permissions,omitempty"`
	Env         map[string]string   `json:"env,omitempty"`
	Model       string              `json:"model,omitempty"`
}

// PermissionDecision represents the outcome of a permission check.
type PermissionDecision string

const (
	PermissionAllow PermissionDecision = "allow"
	PermissionDeny  PermissionDecision = "deny"
	PermissionAsk   PermissionDecision = "ask"
)

// PermissionCheckResult holds the result of checking a tool invocation
// against the loaded permission rules.
type PermissionCheckResult struct {
	Decision PermissionDecision
	Rule     string
	Source   string // "allow", "deny", "ask"
}

// parsedRule is the internal representation of a parsed permission rule string.
type parsedRule struct {
	toolName   string
	argument   string
	isWildcard bool
}

// shellOperators are shell operators that can be used for command
// chaining/injection. These cause a prefix match to fail to prevent
// bypasses like "safe-cmd && malicious-cmd".
var shellOperators = []string{"&&", "||", ";", "|", "$(", "`", "\n"}

// fileEditingTools lists ACP tool names that edit files.
// Per Claude Code docs: "Edit rules apply to all built-in tools that edit files."
var fileEditingTools = []string{
	ACPToolNamePrefix + "Edit",
	ACPToolNamePrefix + "Write",
}

// fileReadingTools lists ACP tool names that read files.
// Per Claude Code docs: "Read rules apply to all built-in tools that read files."
var fileReadingTools = []string{
	ACPToolNamePrefix + "Read",
}

// toolArgAccessors maps tool names to functions that extract the relevant
// argument from tool input for permission matching.
var toolArgAccessors = map[string]func(input map[string]any) string{
	ACPToolNamePrefix + "Read":  func(input map[string]any) string { return getStringArg(input, "file_path") },
	ACPToolNamePrefix + "Edit":  func(input map[string]any) string { return getStringArg(input, "file_path") },
	ACPToolNamePrefix + "Write": func(input map[string]any) string { return getStringArg(input, "file_path") },
	ACPToolNamePrefix + "Bash":  func(input map[string]any) string { return getStringArg(input, "command") },
}

// getStringArg safely extracts a string value from a map.
func getStringArg(input map[string]any, key string) string {
	if v, ok := input[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ruleRegexp matches rule strings like "Read", "Read(./.env)", "Bash(npm run:*)".
var ruleRegexp = regexp.MustCompile(`^(\w+)(?:\((.+)\))?$`)

// parseRule parses a permission rule string into its components.
// Examples:
//
//	"Read"            -> { toolName: "Read" }
//	"Read(./.env)"    -> { toolName: "Read", argument: "./.env" }
//	"Bash(npm run:*)" -> { toolName: "Bash", argument: "npm run", isWildcard: true }
func parseRule(rule string) parsedRule {
	matches := ruleRegexp.FindStringSubmatch(rule)
	if matches == nil {
		return parsedRule{toolName: rule}
	}

	toolName := matches[1]
	argument := matches[2]

	if argument != "" && strings.HasSuffix(argument, ":*") {
		return parsedRule{
			toolName:   toolName,
			argument:   argument[:len(argument)-2],
			isWildcard: true,
		}
	}

	return parsedRule{toolName: toolName, argument: argument}
}

// containsShellOperator checks if a string contains shell operators
// that could allow command chaining.
func containsShellOperator(str string) bool {
	for _, op := range shellOperators {
		if strings.Contains(str, op) {
			return true
		}
	}
	return false
}

// normalizePath normalizes a file path for comparison:
// - Expands ~ to home directory
// - Resolves relative paths against cwd
// - Normalizes path separators
func normalizePath(filePath string, cwd string) string {
	if strings.HasPrefix(filePath, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			filePath = filepath.Join(home, filePath[2:])
		}
	} else if strings.HasPrefix(filePath, "./") {
		filePath = filepath.Join(cwd, filePath[2:])
	} else if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(cwd, filePath)
	}
	cleaned := filepath.Clean(filePath)
	// Convert backslashes to forward slashes for glob compatibility on Windows
	if runtime.GOOS == "windows" {
		cleaned = strings.ReplaceAll(cleaned, "\\", "/")
	}
	return cleaned
}

// matchesGlob checks if a file path matches a glob pattern.
func matchesGlob(pattern string, filePath string, cwd string) bool {
	normalizedPattern := normalizePath(pattern, cwd)
	normalizedPath := normalizePath(filePath, cwd)

	g, err := glob.Compile(normalizedPattern, '/')
	if err != nil {
		return false
	}
	return g.Match(normalizedPath)
}

// matchesRule checks if a tool invocation matches a parsed permission rule.
func matchesRule(rule parsedRule, toolName string, toolInput map[string]any, cwd string) bool {
	// Determine if the rule applies to this tool.
	// - "Bash" rules match the Bash tool
	// - "Edit" rules match all file editing tools
	// - "Read" rules match all file reading tools
	ruleAppliesToTool := false
	switch rule.toolName {
	case "Bash":
		ruleAppliesToTool = (toolName == ACPToolNamePrefix+"Bash")
	case "Edit":
		ruleAppliesToTool = slices.Contains(fileEditingTools, toolName)
	case "Read":
		ruleAppliesToTool = slices.Contains(fileReadingTools, toolName)
	}

	if !ruleAppliesToTool {
		return false
	}

	// Rule with no argument matches all invocations of the tool.
	if rule.argument == "" {
		return true
	}

	argAccessor, ok := toolArgAccessors[toolName]
	if !ok {
		// No accessor means we can't extract the argument; match broadly.
		return true
	}

	actualArg := argAccessor(toolInput)
	if actualArg == "" {
		return false
	}

	// Bash tool: exact match or prefix match with wildcard.
	if toolName == ACPToolNamePrefix+"Bash" {
		if rule.isWildcard {
			if !strings.HasPrefix(actualArg, rule.argument) {
				return false
			}
			remainder := actualArg[len(rule.argument):]
			if containsShellOperator(remainder) {
				return false
			}
			return true
		}
		return actualArg == rule.argument
	}

	// File-based tools: use glob matching.
	return matchesGlob(rule.argument, actualArg, cwd)
}

// loadSettingsFile reads and parses a JSON settings file.
// Returns an empty ClaudeCodeSettings if the file doesn't exist or can't be parsed.
func loadSettingsFile(filePath string) ClaudeCodeSettings {
	if filePath == "" {
		return ClaudeCodeSettings{}
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ClaudeCodeSettings{}
	}
	var settings ClaudeCodeSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return ClaudeCodeSettings{}
	}
	return settings
}

// SettingsManager manages Claude Code settings from multiple sources
// with proper precedence.
//
// Settings are loaded from (in order of increasing precedence):
//  1. User settings (~/.claude/settings.json)
//  2. Project settings (<cwd>/.claude/settings.json)
//  3. Local project settings (<cwd>/.claude/settings.local.json)
//  4. Enterprise managed settings (platform-specific path)
//
// The manager combines permission rules from all sources.
// Deny rules always take precedence during permission checks.
type SettingsManager struct {
	cwd                string
	userSettings       ClaudeCodeSettings
	projectSettings    ClaudeCodeSettings
	localSettings      ClaudeCodeSettings
	enterpriseSettings ClaudeCodeSettings
	mergedSettings     ClaudeCodeSettings
	mu                 sync.RWMutex
	onChange           func()
	logger             *slog.Logger
	initialized        bool
}

// NewSettingsManager creates a new SettingsManager for the given working directory.
func NewSettingsManager(cwd string, logger *slog.Logger) *SettingsManager {
	return &SettingsManager{
		cwd:    cwd,
		logger: logger,
	}
}

// Initialize loads all settings files. Must be called before using
// CheckPermission or GetSettings.
func (s *SettingsManager) Initialize() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.initialized {
		return nil
	}

	s.loadAllSettings()
	s.initialized = true
	return nil
}

// getUserSettingsPath returns the path to the user settings file.
func (s *SettingsManager) getUserSettingsPath() string {
	return filepath.Join(getClaudeConfigDir(), "settings.json")
}

// getProjectSettingsPath returns the path to the project settings file.
func (s *SettingsManager) getProjectSettingsPath() string {
	return filepath.Join(s.cwd, ".claude", "settings.json")
}

// getLocalSettingsPath returns the path to the local project settings file.
func (s *SettingsManager) getLocalSettingsPath() string {
	return filepath.Join(s.cwd, ".claude", "settings.local.json")
}

// loadAllSettings loads settings from all sources and merges them.
func (s *SettingsManager) loadAllSettings() {
	s.userSettings = loadSettingsFile(s.getUserSettingsPath())
	s.projectSettings = loadSettingsFile(s.getProjectSettingsPath())
	s.localSettings = loadSettingsFile(s.getLocalSettingsPath())
	s.enterpriseSettings = loadSettingsFile(getManagedSettingsPath())
	s.mergeSettings()
}

// mergeSettings combines all settings sources with proper precedence.
// For permissions, rules from all sources are combined.
// Deny rules always take precedence during permission checks.
func (s *SettingsManager) mergeSettings() {
	allSettings := []ClaudeCodeSettings{
		s.userSettings,
		s.projectSettings,
		s.localSettings,
		s.enterpriseSettings,
	}

	merged := ClaudeCodeSettings{
		Permissions: &PermissionSettings{
			Allow: []string{},
			Deny:  []string{},
			Ask:   []string{},
		},
	}

	for _, settings := range allSettings {
		if settings.Permissions != nil {
			merged.Permissions.Allow = append(merged.Permissions.Allow, settings.Permissions.Allow...)
			merged.Permissions.Deny = append(merged.Permissions.Deny, settings.Permissions.Deny...)
			merged.Permissions.Ask = append(merged.Permissions.Ask, settings.Permissions.Ask...)
			if len(settings.Permissions.AdditionalDirectories) > 0 {
				merged.Permissions.AdditionalDirectories = append(
					merged.Permissions.AdditionalDirectories,
					settings.Permissions.AdditionalDirectories...,
				)
			}
			if settings.Permissions.DefaultMode != "" {
				merged.Permissions.DefaultMode = settings.Permissions.DefaultMode
			}
		}

		if settings.Env != nil {
			if merged.Env == nil {
				merged.Env = make(map[string]string)
			}
			for k, v := range settings.Env {
				merged.Env[k] = v
			}
		}

		if settings.Model != "" {
			merged.Model = settings.Model
		}
	}

	s.mergedSettings = merged
}

// CheckPermission checks if a tool invocation is allowed based on the
// loaded settings.
//
// Only tools with the ACP prefix (mcp__acp__) are checked.
// Priority: deny > allow > ask > default (ask).
func (s *SettingsManager) CheckPermission(toolName string, toolInput map[string]any) PermissionCheckResult {
	if !strings.HasPrefix(toolName, ACPToolNamePrefix) {
		return PermissionCheckResult{Decision: PermissionAsk}
	}

	s.mu.RLock()
	permissions := s.mergedSettings.Permissions
	cwd := s.cwd
	s.mu.RUnlock()

	if permissions == nil {
		return PermissionCheckResult{Decision: PermissionAsk}
	}

	// Check deny rules first (highest priority).
	for _, rule := range permissions.Deny {
		parsed := parseRule(rule)
		if matchesRule(parsed, toolName, toolInput, cwd) {
			return PermissionCheckResult{
				Decision: PermissionDeny,
				Rule:     rule,
				Source:   "deny",
			}
		}
	}

	// Check allow rules.
	for _, rule := range permissions.Allow {
		parsed := parseRule(rule)
		if matchesRule(parsed, toolName, toolInput, cwd) {
			return PermissionCheckResult{
				Decision: PermissionAllow,
				Rule:     rule,
				Source:   "allow",
			}
		}
	}

	// Check ask rules.
	for _, rule := range permissions.Ask {
		parsed := parseRule(rule)
		if matchesRule(parsed, toolName, toolInput, cwd) {
			return PermissionCheckResult{
				Decision: PermissionAsk,
				Rule:     rule,
				Source:   "ask",
			}
		}
	}

	// No matching rule - default to ask.
	return PermissionCheckResult{Decision: PermissionAsk}
}

// GetSettings returns the current merged settings.
func (s *SettingsManager) GetSettings() ClaudeCodeSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mergedSettings
}

// GetCwd returns the current working directory.
func (s *SettingsManager) GetCwd() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cwd
}

// Dispose cleans up resources held by the SettingsManager.
func (s *SettingsManager) Dispose() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initialized = false
}
