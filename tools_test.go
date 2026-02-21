package main

import (
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestToolInfoFromToolUse_Task(t *testing.T) {
	info := toolInfoFromToolUse("Task", map[string]any{
		"description": "Analyze the codebase",
	})
	if info.Kind != acp.ToolKindThink {
		t.Errorf("expected kind=think, got %v", info.Kind)
	}
	if info.Title != "Analyze the codebase" {
		t.Errorf("expected title from description, got %q", info.Title)
	}
}

func TestToolInfoFromToolUse_Bash(t *testing.T) {
	info := toolInfoFromToolUse("Bash", map[string]any{
		"command": "npm run test",
	})
	if info.Kind != acp.ToolKindExecute {
		t.Errorf("expected kind=execute, got %v", info.Kind)
	}
	if info.Title != "`npm run test`" {
		t.Errorf("expected title with command, got %q", info.Title)
	}
}

func TestToolInfoFromToolUse_ACPBash(t *testing.T) {
	info := toolInfoFromToolUse(ACPToolNames.Bash, map[string]any{
		"command": "ls -la",
	})
	if info.Kind != acp.ToolKindExecute {
		t.Errorf("expected kind=execute, got %v", info.Kind)
	}
}

func TestToolInfoFromToolUse_Read(t *testing.T) {
	info := toolInfoFromToolUse("Read", map[string]any{
		"file_path": "/src/main.go",
	})
	if info.Kind != acp.ToolKindRead {
		t.Errorf("expected kind=read, got %v", info.Kind)
	}
	if len(info.Locations) != 1 || info.Locations[0].Path != "/src/main.go" {
		t.Errorf("expected location with path, got %v", info.Locations)
	}
}

func TestToolInfoFromToolUse_ReadWithRange(t *testing.T) {
	// The plain "Read" tool always returns "Read File" as title
	info := toolInfoFromToolUse("Read", map[string]any{
		"file_path": "/src/main.go",
		"offset":    float64(10),
		"limit":     float64(20),
	})
	if info.Kind != acp.ToolKindRead {
		t.Errorf("expected kind=read, got %v", info.Kind)
	}
	if info.Title != "Read File" {
		t.Errorf("expected title 'Read File', got %q", info.Title)
	}
}

func TestToolInfoFromToolUse_ACPReadWithRange(t *testing.T) {
	// The ACP-prefixed Read tool formats the title with line range
	info := toolInfoFromToolUse(ACPToolNames.Read, map[string]any{
		"file_path": "/src/main.go",
		"offset":    float64(10),
		"limit":     float64(20),
	})
	if info.Kind != acp.ToolKindRead {
		t.Errorf("expected kind=read, got %v", info.Kind)
	}
	expected := "Read /src/main.go (11 - 30)"
	if info.Title != expected {
		t.Errorf("expected title %q, got %q", expected, info.Title)
	}
}

func TestToolInfoFromToolUse_Edit(t *testing.T) {
	info := toolInfoFromToolUse(ACPToolNames.Edit, map[string]any{
		"file_path":  "/src/main.go",
		"old_string": "old code",
		"new_string": "new code",
	})
	if info.Kind != acp.ToolKindEdit {
		t.Errorf("expected kind=edit, got %v", info.Kind)
	}
	if len(info.Content) == 0 {
		t.Error("expected diff content")
	}
	if len(info.Locations) != 1 || info.Locations[0].Path != "/src/main.go" {
		t.Errorf("expected location, got %v", info.Locations)
	}
}

func TestToolInfoFromToolUse_Write(t *testing.T) {
	info := toolInfoFromToolUse(ACPToolNames.Write, map[string]any{
		"file_path": "/src/new.go",
		"content":   "package main",
	})
	if info.Kind != acp.ToolKindEdit {
		t.Errorf("expected kind=edit, got %v", info.Kind)
	}
}

func TestToolInfoFromToolUse_Glob(t *testing.T) {
	info := toolInfoFromToolUse("Glob", map[string]any{
		"pattern": "**/*.go",
		"path":    "/src",
	})
	if info.Kind != acp.ToolKindSearch {
		t.Errorf("expected kind=search, got %v", info.Kind)
	}
}

func TestToolInfoFromToolUse_Grep(t *testing.T) {
	info := toolInfoFromToolUse("Grep", map[string]any{
		"pattern": "func main",
		"path":    "/src",
		"-i":      true,
	})
	if info.Kind != acp.ToolKindSearch {
		t.Errorf("expected kind=search, got %v", info.Kind)
	}
	if info.Title == "" {
		t.Error("expected non-empty title")
	}
}

func TestToolInfoFromToolUse_WebFetch(t *testing.T) {
	info := toolInfoFromToolUse("WebFetch", map[string]any{
		"url": "https://example.com",
	})
	if info.Kind != acp.ToolKindFetch {
		t.Errorf("expected kind=fetch, got %v", info.Kind)
	}
	if info.Title != "Fetch https://example.com" {
		t.Errorf("expected title with URL, got %q", info.Title)
	}
}

func TestToolInfoFromToolUse_WebSearch(t *testing.T) {
	info := toolInfoFromToolUse("WebSearch", map[string]any{
		"query": "golang testing",
	})
	if info.Kind != acp.ToolKindFetch {
		t.Errorf("expected kind=fetch, got %v", info.Kind)
	}
}

func TestToolInfoFromToolUse_TodoWrite(t *testing.T) {
	info := toolInfoFromToolUse("TodoWrite", map[string]any{
		"todos": []any{
			map[string]any{"content": "Fix bug"},
			map[string]any{"content": "Add tests"},
		},
	})
	if info.Kind != acp.ToolKindThink {
		t.Errorf("expected kind=think, got %v", info.Kind)
	}
}

func TestToolInfoFromToolUse_ExitPlanMode(t *testing.T) {
	info := toolInfoFromToolUse("ExitPlanMode", map[string]any{
		"plan": "My plan here",
	})
	if info.Kind != acp.ToolKindSwitchMode {
		t.Errorf("expected kind=switch_mode, got %v", info.Kind)
	}
}

func TestToolInfoFromToolUse_Unknown(t *testing.T) {
	info := toolInfoFromToolUse("SomeUnknownTool", map[string]any{})
	if info.Kind != acp.ToolKindOther {
		t.Errorf("expected kind=other, got %v", info.Kind)
	}
	if info.Title != "SomeUnknownTool" {
		t.Errorf("expected title=SomeUnknownTool, got %q", info.Title)
	}
}

func TestPlanEntries(t *testing.T) {
	todos := []ClaudePlanEntry{
		{Content: "Step 1", Status: "completed"},
		{Content: "Step 2", Status: "in_progress"},
		{Content: "Step 3", Status: "pending"},
	}
	entries := planEntries(todos)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Content != "Step 1" {
		t.Errorf("expected content 'Step 1', got %q", entries[0].Content)
	}
	if entries[0].Status != acp.PlanEntryStatus("completed") {
		t.Errorf("expected status completed, got %v", entries[0].Status)
	}
	if entries[1].Status != acp.PlanEntryStatus("in_progress") {
		t.Errorf("expected status in_progress, got %v", entries[1].Status)
	}
}

func TestToolUpdateFromToolResult_ReadTool(t *testing.T) {
	toolUse := &ToolUseEntry{Name: "Read", ID: "123"}
	result := map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": "file content here"},
		},
	}
	update := toolUpdateFromToolResult(result, toolUse)
	if len(update.Content) == 0 {
		t.Error("expected content in update")
	}
}

func TestToolUpdateFromToolResult_Error(t *testing.T) {
	toolUse := &ToolUseEntry{Name: "Read", ID: "123"}
	result := map[string]any{
		"is_error": true,
		"content":  "Something went wrong",
	}
	update := toolUpdateFromToolResult(result, toolUse)
	if len(update.Content) == 0 {
		t.Error("expected error content in update")
	}
}

func TestToolUpdateFromToolResult_ExitPlanMode(t *testing.T) {
	toolUse := &ToolUseEntry{Name: "ExitPlanMode", ID: "123"}
	result := map[string]any{
		"content": "ok",
	}
	update := toolUpdateFromToolResult(result, toolUse)
	if update.Title == nil || *update.Title != "Exited Plan Mode" {
		t.Error("expected title 'Exited Plan Mode'")
	}
}

func TestParseUnifiedDiff(t *testing.T) {
	diff := `--- a/file.go
+++ b/file.go
@@ -1,3 +1,4 @@
 line1
+new line
 line2
 line3`

	patches := parseUnifiedDiff(diff)
	if len(patches) != 1 {
		t.Fatalf("expected 1 patch, got %d", len(patches))
	}
	if patches[0].oldFileName != "a/file.go" {
		t.Errorf("expected oldFileName=a/file.go, got %q", patches[0].oldFileName)
	}
	if patches[0].newFileName != "b/file.go" {
		t.Errorf("expected newFileName=b/file.go, got %q", patches[0].newFileName)
	}
	if len(patches[0].hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(patches[0].hunks))
	}
	if patches[0].hunks[0].newStart != 1 {
		t.Errorf("expected newStart=1, got %d", patches[0].hunks[0].newStart)
	}
}

func TestToAcpNotifications_TextContent(t *testing.T) {
	cache := make(map[string]ToolUseEntry)
	notifications := toAcpNotifications("hello world", "assistant", "session-1", cache, nil)
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifications))
	}
	if notifications[0].Update.AgentMessageChunk == nil {
		t.Error("expected agent message chunk")
	}
}

func TestToAcpNotifications_ThinkingBlock(t *testing.T) {
	cache := make(map[string]ToolUseEntry)
	blocks := []any{
		map[string]any{"type": "thinking", "thinking": "Let me think..."},
	}
	notifications := toAcpNotifications(blocks, "assistant", "session-1", cache, nil)
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifications))
	}
	if notifications[0].Update.AgentThoughtChunk == nil {
		t.Error("expected agent thought chunk")
	}
}

func TestToAcpNotifications_ToolUseBlock(t *testing.T) {
	cache := make(map[string]ToolUseEntry)
	blocks := []any{
		map[string]any{
			"type":  "tool_use",
			"id":    "tool-1",
			"name":  "Read",
			"input": map[string]any{"file_path": "/test.go"},
		},
	}
	notifications := toAcpNotifications(blocks, "assistant", "session-1", cache, nil)
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifications))
	}
	if notifications[0].Update.ToolCall == nil {
		t.Error("expected tool call update")
	}
	// Verify it was cached
	if _, ok := cache["tool-1"]; !ok {
		t.Error("expected tool use to be cached")
	}
}

func TestStreamEventToAcpNotifications_ContentBlockStart(t *testing.T) {
	cache := make(map[string]ToolUseEntry)
	msg := map[string]any{
		"event": map[string]any{
			"type": "content_block_start",
			"content_block": map[string]any{
				"type": "text",
				"text": "Hello",
			},
		},
	}
	notifications := streamEventToAcpNotifications(msg, "session-1", cache, nil)
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifications))
	}
}

func TestStreamEventToAcpNotifications_MessageStop(t *testing.T) {
	cache := make(map[string]ToolUseEntry)
	msg := map[string]any{
		"event": map[string]any{
			"type": "message_stop",
		},
	}
	notifications := streamEventToAcpNotifications(msg, "session-1", cache, nil)
	if len(notifications) != 0 {
		t.Errorf("expected 0 notifications for message_stop, got %d", len(notifications))
	}
}
