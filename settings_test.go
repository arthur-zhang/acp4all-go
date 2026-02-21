package main

import (
	"testing"
)

func TestParseRule_SimpleToolName(t *testing.T) {
	rule := parseRule("Read")
	if rule.toolName != "Read" {
		t.Errorf("expected toolName=Read, got %q", rule.toolName)
	}
	if rule.argument != "" {
		t.Errorf("expected empty argument, got %q", rule.argument)
	}
	if rule.isWildcard {
		t.Error("expected isWildcard=false")
	}
}

func TestParseRule_WithArgument(t *testing.T) {
	rule := parseRule("Read(./.env)")
	if rule.toolName != "Read" {
		t.Errorf("expected toolName=Read, got %q", rule.toolName)
	}
	if rule.argument != "./.env" {
		t.Errorf("expected argument=./.env, got %q", rule.argument)
	}
	if rule.isWildcard {
		t.Error("expected isWildcard=false")
	}
}

func TestParseRule_WithWildcard(t *testing.T) {
	rule := parseRule("Bash(npm run:*)")
	if rule.toolName != "Bash" {
		t.Errorf("expected toolName=Bash, got %q", rule.toolName)
	}
	if rule.argument != "npm run" {
		t.Errorf("expected argument='npm run', got %q", rule.argument)
	}
	if !rule.isWildcard {
		t.Error("expected isWildcard=true")
	}
}

func TestParseRule_WithGlobPattern(t *testing.T) {
	rule := parseRule("Read(./.env.*)")
	if rule.toolName != "Read" {
		t.Errorf("expected toolName=Read, got %q", rule.toolName)
	}
	if rule.argument != "./.env.*" {
		t.Errorf("expected argument=./.env.*, got %q", rule.argument)
	}
}

func TestContainsShellOperator(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"safe command", false},
		{"cmd && malicious", true},
		{"cmd || other", true},
		{"cmd; other", true},
		{"cmd | other", true},
		{"$(malicious)", true},
		{"`malicious`", true},
		{"cmd\nother", true},
		{"safe-command", false},
	}
	for _, tt := range tests {
		got := containsShellOperator(tt.input)
		if got != tt.expected {
			t.Errorf("containsShellOperator(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestMatchesRule_BashExactMatch(t *testing.T) {
	rule := parsedRule{toolName: "Bash", argument: "npm run lint", isWildcard: false}
	toolInput := map[string]any{"command": "npm run lint"}

	if !matchesRule(rule, ACPToolNamePrefix+"Bash", toolInput, "/test") {
		t.Error("expected exact match to succeed")
	}

	toolInput2 := map[string]any{"command": "npm run test"}
	if matchesRule(rule, ACPToolNamePrefix+"Bash", toolInput2, "/test") {
		t.Error("expected different command to not match")
	}
}

func TestMatchesRule_BashPrefixMatch(t *testing.T) {
	rule := parsedRule{toolName: "Bash", argument: "npm run", isWildcard: true}

	tests := []struct {
		command  string
		expected bool
	}{
		{"npm run lint", true},
		{"npm run test", true},
		{"npm run", true},
		{"npm install", false},
		{"npm run && malicious", false}, // shell operator blocks prefix match
	}

	for _, tt := range tests {
		toolInput := map[string]any{"command": tt.command}
		got := matchesRule(rule, ACPToolNamePrefix+"Bash", toolInput, "/test")
		if got != tt.expected {
			t.Errorf("matchesRule with command %q = %v, want %v", tt.command, got, tt.expected)
		}
	}
}

func TestMatchesRule_EditToolAppliesTo(t *testing.T) {
	rule := parsedRule{toolName: "Edit", argument: "", isWildcard: false}

	// Edit rule should match both Edit and Write tools
	if !matchesRule(rule, ACPToolNamePrefix+"Edit", map[string]any{}, "/test") {
		t.Error("Edit rule should match Edit tool")
	}
	if !matchesRule(rule, ACPToolNamePrefix+"Write", map[string]any{}, "/test") {
		t.Error("Edit rule should match Write tool")
	}
	if matchesRule(rule, ACPToolNamePrefix+"Read", map[string]any{}, "/test") {
		t.Error("Edit rule should not match Read tool")
	}
}

func TestMatchesRule_ReadToolAppliesTo(t *testing.T) {
	rule := parsedRule{toolName: "Read", argument: "", isWildcard: false}

	if !matchesRule(rule, ACPToolNamePrefix+"Read", map[string]any{}, "/test") {
		t.Error("Read rule should match Read tool")
	}
	if matchesRule(rule, ACPToolNamePrefix+"Edit", map[string]any{}, "/test") {
		t.Error("Read rule should not match Edit tool")
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		path     string
		cwd      string
		expected string
	}{
		{"./file.txt", "/home/user", "/home/user/file.txt"},
		{"/abs/path.txt", "/home/user", "/abs/path.txt"},
		{"file.txt", "/home/user", "/home/user/file.txt"},
	}

	for _, tt := range tests {
		got := normalizePath(tt.path, tt.cwd)
		if got != tt.expected {
			t.Errorf("normalizePath(%q, %q) = %q, want %q", tt.path, tt.cwd, got, tt.expected)
		}
	}
}

func TestPermissionCheckResult_Priority(t *testing.T) {
	// Test that deny > allow > ask priority is enforced
	mgr := &SettingsManager{
		cwd: "/test",
		mergedSettings: ClaudeCodeSettings{
			Permissions: &PermissionSettings{
				Deny:  []string{"Read(./.env)"},
				Allow: []string{"Read"},
				Ask:   []string{"Read(./*)"},
			},
		},
	}

	// Deny should take precedence
	result := mgr.CheckPermission(ACPToolNamePrefix+"Read", map[string]any{"file_path": "./.env"})
	if result.Decision != PermissionDeny {
		t.Errorf("expected deny, got %v", result.Decision)
	}

	// Allow should apply when no deny matches
	result2 := mgr.CheckPermission(ACPToolNamePrefix+"Read", map[string]any{"file_path": "./other.txt"})
	if result2.Decision != PermissionAllow {
		t.Errorf("expected allow, got %v", result2.Decision)
	}
}

func TestPermissionCheckResult_NonACPTool(t *testing.T) {
	mgr := &SettingsManager{
		cwd: "/test",
		mergedSettings: ClaudeCodeSettings{
			Permissions: &PermissionSettings{
				Deny: []string{"Read"},
			},
		},
	}

	// Non-ACP tools should always return ask
	result := mgr.CheckPermission("SomeOtherTool", map[string]any{})
	if result.Decision != PermissionAsk {
		t.Errorf("expected ask for non-ACP tool, got %v", result.Decision)
	}
}
