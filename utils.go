package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// ExtractLinesResult holds the result of extracting lines with a limit.
type ExtractLinesResult struct {
	Content    string
	WasLimited bool
	LinesRead  int
}

// extractLinesWithByteLimit extracts lines from content up to
// maxContentLength characters. The first line is always included
// even if it exceeds the limit.
func extractLinesWithByteLimit(fullContent string, maxContentLength int) ExtractLinesResult {
	if fullContent == "" {
		return ExtractLinesResult{
			Content:    "",
			WasLimited: false,
			LinesRead:  1,
		}
	}

	linesSeen := 0
	index := 0
	contentLength := 0
	wasLimited := false

	for {
		nextIndex := strings.Index(fullContent[index:], "\n")

		if nextIndex < 0 {
			// Last line in file (no trailing newline)
			if linesSeen > 0 && len(fullContent) > maxContentLength {
				wasLimited = true
				break
			}
			linesSeen++
			contentLength = len(fullContent)
			break
		}

		// Adjust nextIndex to be absolute
		nextIndex += index

		// Line with newline - include up to the newline
		newContentLength := nextIndex + 1
		if linesSeen > 0 && newContentLength > maxContentLength {
			wasLimited = true
			break
		}
		linesSeen++
		contentLength = newContentLength
		index = newContentLength
	}

	return ExtractLinesResult{
		Content:    fullContent[:contentLength],
		WasLimited: wasLimited,
		LinesRead:  linesSeen,
	}
}

// getManagedSettingsPath returns the platform-specific path for
// managed (enterprise) settings.
func getManagedSettingsPath() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode/managed-settings.json"
	case "linux":
		return "/etc/claude-code/managed-settings.json"
	case "windows":
		return `C:\Program Files\ClaudeCode\managed-settings.json`
	default:
		return "/etc/claude-code/managed-settings.json"
	}
}

// loadManagedSettings reads and parses the managed settings file.
// Returns nil if the file doesn't exist or can't be parsed.
func loadManagedSettings() *ClaudeCodeSettings {
	data, err := os.ReadFile(getManagedSettingsPath())
	if err != nil {
		return nil
	}
	var settings ClaudeCodeSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil
	}
	return &settings
}

// applyEnvironmentSettings sets environment variables from the
// settings Env map.
func applyEnvironmentSettings(settings *ClaudeCodeSettings) {
	if settings.Env == nil {
		return
	}
	for key, value := range settings.Env {
		os.Setenv(key, value)
	}
}

// decodeProjectPath decodes an encoded project path by replacing
// the leading dash with "/" and all remaining dashes with "/".
// e.g. "-Users-morse-project" -> "/Users/morse/project"
func decodeProjectPath(encoded string) string {
	if len(encoded) == 0 {
		return encoded
	}
	if encoded[0] == '-' {
		return "/" + strings.ReplaceAll(encoded[1:], "-", "/")
	}
	return strings.ReplaceAll(encoded, "-", "/")
}

// sanitizeTitle normalizes whitespace and truncates text to maxLen runes,
// appending "…" if the text was truncated.
func sanitizeTitle(text string, maxLen int) string {
	// Replace newlines and collapse whitespace (matching TS behavior)
	sanitized := strings.Join(strings.Fields(text), " ")
	runes := []rune(sanitized)
	if len(runes) <= maxLen {
		return sanitized
	}
	if maxLen <= 1 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-1]) + "…"
}

// markdownEscape wraps text in a markdown code fence, using a
// fence length that is one backtick longer than the longest
// sequence of backticks found at the start of any line in the text.
func markdownEscape(text string) string {
	// Match backtick sequences at the start of lines (matching TS /^```+/gm)
	fence := "```"
	for _, match := range markdownFenceRe.FindAllString(text, -1) {
		for len(match) >= len(fence) {
			fence += "`"
		}
	}
	trailing := ""
	if !strings.HasSuffix(text, "\n") {
		trailing = "\n"
	}
	return fence + "\n" + text + trailing + fence
}

var markdownFenceRe = regexp.MustCompile("(?m)^`{3,}")

// getClaudeConfigDir returns the path to the ~/.claude directory.
// Supports CLAUDE_CONFIG_DIR environment variable override.
func getClaudeConfigDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("/", ".claude")
	}
	return filepath.Join(home, ".claude")
}
