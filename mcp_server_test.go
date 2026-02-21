package main

import (
	"strings"
	"testing"
)

// TestMcpServer_ReplaceAndCalculateLocation tests the edit replacement logic
func TestMcpServer_ReplaceAndCalculateLocation(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		edits       []EditOperation
		expected    string
		expectErr   bool
		expectLines int
	}{
		{
			name:    "simple replacement",
			content: "hello world",
			edits: []EditOperation{
				{OldText: "world", NewText: "Go"},
			},
			expected:    "hello Go",
			expectLines: 1,
		},
		{
			name:    "multiline replacement",
			content: "line1\nline2\nline3",
			edits: []EditOperation{
				{OldText: "line2", NewText: "replaced"},
			},
			expected:    "line1\nreplaced\nline3",
			expectLines: 1,
		},
		{
			name:    "replace all occurrences",
			content: "foo bar foo baz foo",
			edits: []EditOperation{
				{OldText: "foo", NewText: "qux", ReplaceAll: true},
			},
			expected:    "qux bar qux baz qux",
			expectLines: 1, // all on same line, deduped
		},
		{
			name:    "empty old_string should error",
			content: "hello",
			edits: []EditOperation{
				{OldText: "", NewText: "world"},
			},
			expectErr: true,
		},
		{
			name:    "old_string not found should error",
			content: "hello world",
			edits: []EditOperation{
				{OldText: "missing", NewText: "replacement"},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, lines, err := replaceAndCalculateLocation(tt.content, tt.edits)
			if tt.expectErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("content mismatch:\ngot:  %q\nwant: %q", result, tt.expected)
			}
			if tt.expectLines > 0 && len(lines) != tt.expectLines {
				t.Errorf("expected %d line numbers, got %d: %v", tt.expectLines, len(lines), lines)
			}
		})
	}
}

// TestMcpServer_CreateUnifiedDiff tests unified diff generation
func TestMcpServer_CreateUnifiedDiff(t *testing.T) {
	tests := []struct {
		name       string
		filename   string
		oldContent string
		newContent string
		wantEmpty  bool
		wantParts  []string // substrings that should appear in the diff
	}{
		{
			name:       "no changes",
			filename:   "test.go",
			oldContent: "hello\nworld",
			newContent: "hello\nworld",
			wantEmpty:  true,
		},
		{
			name:       "single line addition",
			filename:   "test.go",
			oldContent: "line1\nline3",
			newContent: "line1\nline2\nline3",
			wantParts:  []string{"--- a/test.go", "+++ b/test.go", "+line2"},
		},
		{
			name:       "single line deletion",
			filename:   "test.go",
			oldContent: "line1\nline2\nline3",
			newContent: "line1\nline3",
			wantParts:  []string{"-line2"},
		},
		{
			name:       "line modification",
			filename:   "test.go",
			oldContent: "hello world",
			newContent: "hello Go",
			wantParts:  []string{"-hello world", "+hello Go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff := createUnifiedDiff(tt.filename, tt.oldContent, tt.newContent)
			if tt.wantEmpty {
				if diff != "" {
					t.Errorf("expected empty diff, got:\n%s", diff)
				}
				return
			}
			if diff == "" {
				t.Error("expected non-empty diff")
				return
			}
			for _, part := range tt.wantParts {
				if !strings.Contains(diff, part) {
					t.Errorf("diff missing %q:\n%s", part, diff)
				}
			}
		})
	}
}

// TestMcpServer_FormatToolCommandOutput tests terminal output formatting
func TestMcpServer_FormatToolCommandOutput(t *testing.T) {
	exitCode0 := 0
	exitCode1 := 1

	tests := []struct {
		name      string
		status    string
		output    string
		exitCode  *int
		signal    string
		truncated bool
		wantParts []string
	}{
		{
			name:      "normal exit",
			status:    "exited",
			output:    "hello world",
			exitCode:  &exitCode0,
			wantParts: []string{"Exited with code 0", "hello world"},
		},
		{
			name:      "error exit",
			status:    "exited",
			output:    "error occurred",
			exitCode:  &exitCode1,
			wantParts: []string{"Exited with code 1", "error occurred"},
		},
		{
			name:      "timed out",
			status:    "timedOut",
			output:    "partial output",
			wantParts: []string{"Timed out", "partial output"},
		},
		{
			name:      "killed",
			status:    "killed",
			output:    "",
			wantParts: []string{"Killed"},
		},
		{
			name:      "truncated output",
			status:    "exited",
			output:    "long output",
			exitCode:  &exitCode0,
			truncated: true,
			wantParts: []string{"truncated"},
		},
		{
			name:      "signal",
			status:    "exited",
			output:    "",
			signal:    "SIGTERM",
			wantParts: []string{"Signal `SIGTERM`"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatToolCommandOutput(tt.status, tt.output, tt.exitCode, tt.signal, tt.truncated)
			for _, part := range tt.wantParts {
				if !strings.Contains(result, part) {
					t.Errorf("output missing %q:\n%s", part, result)
				}
			}
		})
	}
}

// TestMcpServer_IsInternalPath tests internal path detection
func TestMcpServer_IsInternalPath(t *testing.T) {
	claudeDir := getClaudeConfigDir()

	tests := []struct {
		path     string
		expected bool
	}{
		{claudeDir + "/projects/test.jsonl", true},
		{claudeDir + "/settings.json", false},
		{claudeDir + "/session-env/test", false},
		{"/tmp/other/file.txt", false},
		{claudeDir + "/todos/test.json", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isInternalPath(tt.path)
			if got != tt.expected {
				t.Errorf("isInternalPath(%q) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}

// TestMcpServer_StripCommonPrefix tests common prefix stripping
func TestMcpServer_StripCommonPrefix(t *testing.T) {
	tests := []struct {
		a, b     string
		expected string
	}{
		{"hello", "hello world", " world"},
		{"abc", "abcdef", "def"},
		{"", "hello", "hello"},
		{"xyz", "abc", "abc"},
		{"same", "same", ""},
	}

	for _, tt := range tests {
		got := stripCommonPrefix(tt.a, tt.b)
		if got != tt.expected {
			t.Errorf("stripCommonPrefix(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.expected)
		}
	}
}

// TestMcpServer_SplitLines tests line splitting
func TestMcpServer_SplitLines(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"hello", 1},
		{"hello\nworld", 2},
		{"a\nb\nc\n", 4}, // trailing newline creates empty element
	}

	for _, tt := range tests {
		got := splitLines(tt.input)
		if len(got) != tt.expected {
			t.Errorf("splitLines(%q) = %d lines, want %d", tt.input, len(got), tt.expected)
		}
	}
}
