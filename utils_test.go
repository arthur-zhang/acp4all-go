package main

import (
	"strings"
	"testing"
)

func TestExtractLinesWithByteLimit_EmptyString(t *testing.T) {
	result := extractLinesWithByteLimit("", 100)
	if result.Content != "" {
		t.Errorf("expected empty content, got %q", result.Content)
	}
	if result.WasLimited {
		t.Error("expected WasLimited=false")
	}
	if result.LinesRead != 1 {
		t.Errorf("expected LinesRead=1, got %d", result.LinesRead)
	}
}

func TestExtractLinesWithByteLimit_SingleLineUnderLimit(t *testing.T) {
	result := extractLinesWithByteLimit("hello", 100)
	if result.Content != "hello" {
		t.Errorf("expected %q, got %q", "hello", result.Content)
	}
	if result.WasLimited {
		t.Error("expected WasLimited=false")
	}
	if result.LinesRead != 1 {
		t.Errorf("expected LinesRead=1, got %d", result.LinesRead)
	}
}

func TestExtractLinesWithByteLimit_MultipleLines(t *testing.T) {
	content := "line1\nline2\nline3\n"
	result := extractLinesWithByteLimit(content, 100)
	if result.Content != content {
		t.Errorf("expected full content, got %q", result.Content)
	}
	if result.WasLimited {
		t.Error("expected WasLimited=false")
	}
	// Trailing newline creates an empty 4th "line" after the last \n
	if result.LinesRead != 4 {
		t.Errorf("expected LinesRead=4, got %d", result.LinesRead)
	}
}

func TestExtractLinesWithByteLimit_NoTrailingNewline(t *testing.T) {
	content := "line1\nline2\nline3"
	result := extractLinesWithByteLimit(content, 100)
	if result.Content != content {
		t.Errorf("expected full content, got %q", result.Content)
	}
	if result.WasLimited {
		t.Error("expected WasLimited=false")
	}
	if result.LinesRead != 3 {
		t.Errorf("expected LinesRead=3, got %d", result.LinesRead)
	}
}

func TestExtractLinesWithByteLimit_TruncatesAtLimit(t *testing.T) {
	content := "line1\nline2\nline3\n"
	result := extractLinesWithByteLimit(content, 12) // "line1\nline2\n" = 12 chars
	if result.Content != "line1\nline2\n" {
		t.Errorf("expected %q, got %q", "line1\nline2\n", result.Content)
	}
	if !result.WasLimited {
		t.Error("expected WasLimited=true")
	}
	if result.LinesRead != 2 {
		t.Errorf("expected LinesRead=2, got %d", result.LinesRead)
	}
}

func TestExtractLinesWithByteLimit_FirstLineAlwaysIncluded(t *testing.T) {
	content := "a very long first line\nsecond\n"
	result := extractLinesWithByteLimit(content, 5)
	if result.Content != "a very long first line\n" {
		t.Errorf("expected first line included, got %q", result.Content)
	}
	if !result.WasLimited {
		t.Error("expected WasLimited=true")
	}
	if result.LinesRead != 1 {
		t.Errorf("expected LinesRead=1, got %d", result.LinesRead)
	}
}

func TestDecodeProjectPath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"-Users-morse-project", "/Users/morse/project"},
		{"", ""},
		{"C-Users-morse", "C/Users/morse"},
	}
	for _, tt := range tests {
		got := decodeProjectPath(tt.input)
		if got != tt.expected {
			t.Errorf("decodeProjectPath(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestSanitizeTitle(t *testing.T) {
	tests := []struct {
		text     string
		maxLen   int
		expected string
	}{
		{"short", 10, "short"},
		{"a long title here", 10, "a long ti…"}, // 10 runes: 9 chars + "…"
		{"abc", 3, "abc"},
		{"abcd", 3, "ab…"},
		{"hello world", 5, "hell…"},
		{"hello\nworld", 20, "hello world"},       // newline collapsed
		{"  hello   world  ", 20, "hello world"},   // whitespace collapsed and trimmed
		{"line1\r\nline2", 20, "line1 line2"},      // \r\n collapsed
	}
	for _, tt := range tests {
		got := sanitizeTitle(tt.text, tt.maxLen)
		if got != tt.expected {
			t.Errorf("sanitizeTitle(%q, %d) = %q, want %q", tt.text, tt.maxLen, got, tt.expected)
		}
	}
}

func TestMarkdownEscape_NoBackticks(t *testing.T) {
	result := markdownEscape("hello world")
	if !strings.HasPrefix(result, "```\n") {
		t.Errorf("expected to start with ```, got %q", result[:10])
	}
	if !strings.HasSuffix(result, "\n```") {
		t.Errorf("expected to end with ```, got %q", result)
	}
	if !strings.Contains(result, "hello world") {
		t.Error("expected content to be preserved")
	}
}

func TestMarkdownEscape_WithBackticksNotAtLineStart(t *testing.T) {
	// Backticks not at line start should NOT trigger longer fence (matching TS /^```+/gm)
	result := markdownEscape("some ```code``` here")
	if !strings.HasPrefix(result, "```\n") {
		t.Errorf("expected to start with ``` (3 backticks), got prefix: %q", result[:10])
	}
}

func TestMarkdownEscape_WithBackticksAtLineStart(t *testing.T) {
	// Backticks at line start should trigger longer fence
	result := markdownEscape("some text\n```code```\nmore")
	if !strings.HasPrefix(result, "````\n") {
		t.Errorf("expected to start with ```` (4 backticks), got prefix: %q", result[:10])
	}
}

func TestMarkdownEscape_WithLongBackticksAtLineStart(t *testing.T) {
	result := markdownEscape("`````code`````")
	// Should use 6 backticks since content starts with 5 at line start
	if !strings.HasPrefix(result, "``````\n") {
		t.Errorf("expected 6-backtick fence, got prefix: %q", result[:12])
	}
}

func TestMarkdownEscape_TrailingNewline(t *testing.T) {
	// Text ending with newline should NOT add extra newline
	result := markdownEscape("hello\n")
	expected := "```\nhello\n```"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestMarkdownEscape_NoTrailingNewline(t *testing.T) {
	// Text not ending with newline should add one
	result := markdownEscape("hello")
	expected := "```\nhello\n```"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}
