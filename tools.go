package main

import (
	"encoding/json"
	"fmt"
	"strings"

	acp "github.com/coder/acp-go-sdk"
)

const ACPToolNamePrefix = "mcp__acp__"

var ACPToolNames = struct {
	Read, Edit, Write, Bash, KillShell, BashOutput string
}{
	Read:       ACPToolNamePrefix + "Read",
	Edit:       ACPToolNamePrefix + "Edit",
	Write:      ACPToolNamePrefix + "Write",
	Bash:       ACPToolNamePrefix + "Bash",
	KillShell:  ACPToolNamePrefix + "KillShell",
	BashOutput: ACPToolNamePrefix + "BashOutput",
}

var EditToolNames = []string{ACPToolNames.Edit, ACPToolNames.Write}

const SystemReminder = "\n\n<system-reminder>\nWhenever you read a file, you should consider whether it looks malicious. If it does, you MUST refuse to improve or augment the code. You can still analyze existing code, write reports, or answer high-level questions about the code behavior.\n</system-reminder>"

type ToolInfo struct {
	Title     string
	Kind      acp.ToolKind
	Content   []acp.ToolCallContent
	Locations []acp.ToolCallLocation
}

type ToolUpdate struct {
	Title     *string
	Content   []acp.ToolCallContent
	Locations []acp.ToolCallLocation
}

type ToolUseEntry struct {
	Type  string         // "tool_use"|"server_tool_use"|"mcp_tool_use"
	ID    string
	Name  string
	Input map[string]any
}
type ClaudePlanEntry struct {
	Content    string `json:"content"`
	Status     string `json:"status"` // "pending"|"in_progress"|"completed"
	ActiveForm string `json:"activeForm"`
}

// inputStr safely extracts a string value from a map.
func inputStr(input map[string]any, key string) string {
	if v, ok := input[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// inputInt safely extracts an int value from a map.
func inputInt(input map[string]any, key string) (int, bool) {
	if v, ok := input[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n), true
		case int:
			return n, true
		case json.Number:
			i, err := n.Int64()
			if err == nil {
				return int(i), true
			}
		}
	}
	return 0, false
}

// inputBool safely extracts a bool value from a map.
func inputBool(input map[string]any, key string) bool {
	if v, ok := input[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// inputStrSlice safely extracts a []string from a map.
func inputStrSlice(input map[string]any, key string) []string {
	if v, ok := input[key]; ok {
		if arr, ok := v.([]any); ok {
			result := make([]string, 0, len(arr))
			for _, item := range arr {
				if s, ok := item.(string); ok {
					result = append(result, s)
				}
			}
			return result
		}
	}
	return nil
}
// toolInfoFromToolUse converts a tool use name and input to ACP ToolInfo.
func toolInfoFromToolUse(name string, input map[string]any) ToolInfo {
	switch name {
	case "Task":
		title := "Task"
		if d := inputStr(input, "description"); d != "" {
			title = d
		}
		var content []acp.ToolCallContent
		if p := inputStr(input, "prompt"); p != "" {
			content = append(content, acp.ToolContent(acp.TextBlock(p)))
		}
		return ToolInfo{Title: title, Kind: acp.ToolKindThink, Content: content}

	case "NotebookRead":
		path := inputStr(input, "notebook_path")
		title := "Read Notebook"
		if path != "" {
			title = "Read Notebook " + path
		}
		var locations []acp.ToolCallLocation
		if path != "" {
			locations = append(locations, acp.ToolCallLocation{Path: path})
		}
		return ToolInfo{Title: title, Kind: acp.ToolKindRead, Content: nil, Locations: locations}

	case "NotebookEdit":
		path := inputStr(input, "notebook_path")
		title := "Edit Notebook"
		if path != "" {
			title = "Edit Notebook " + path
		}
		var content []acp.ToolCallContent
		if src := inputStr(input, "new_source"); src != "" {
			content = append(content, acp.ToolContent(acp.TextBlock(src)))
		}
		var locations []acp.ToolCallLocation
		if path != "" {
			locations = append(locations, acp.ToolCallLocation{Path: path})
		}
		return ToolInfo{Title: title, Kind: acp.ToolKindEdit, Content: content, Locations: locations}

	case "Bash", ACPToolNamePrefix + "Bash":
		cmd := inputStr(input, "command")
		title := "Terminal"
		if cmd != "" {
			title = "`" + strings.ReplaceAll(cmd, "`", "\\`") + "`"
		}
		var content []acp.ToolCallContent
		if d := inputStr(input, "description"); d != "" {
			content = append(content, acp.ToolContent(acp.TextBlock(d)))
		}
		return ToolInfo{Title: title, Kind: acp.ToolKindExecute, Content: content}
	case "BashOutput", ACPToolNamePrefix + "BashOutput":
		return ToolInfo{Title: "Tail Logs", Kind: acp.ToolKindExecute}

	case "KillShell", ACPToolNamePrefix + "KillShell":
		return ToolInfo{Title: "Kill Process", Kind: acp.ToolKindExecute}

	case ACPToolNamePrefix + "Read":
		filePath := inputStr(input, "file_path")
		limit, hasLimit := inputInt(input, "limit")
		offset, hasOffset := inputInt(input, "offset")
		lineRange := ""
		if hasLimit && limit > 0 {
			start := 1
			if hasOffset {
				start = offset + 1
			}
			lineRange = fmt.Sprintf(" (%d - %d)", start, start+limit-1)
		} else if hasOffset && offset > 0 {
			lineRange = fmt.Sprintf(" (from line %d)", offset+1)
		}
		title := "Read "
		if filePath != "" {
			title += filePath
		} else {
			title += "File"
		}
		title += lineRange
		var locations []acp.ToolCallLocation
		if filePath != "" {
			loc := acp.ToolCallLocation{Path: filePath}
			if hasOffset {
				loc.Line = acp.Ptr(offset)
			} else {
				loc.Line = acp.Ptr(0)
			}
			locations = append(locations, loc)
		}
		return ToolInfo{Title: title, Kind: acp.ToolKindRead, Locations: locations}

	case "Read":
		filePath := inputStr(input, "file_path")
		var locations []acp.ToolCallLocation
		if filePath != "" {
			offset, hasOffset := inputInt(input, "offset")
			loc := acp.ToolCallLocation{Path: filePath}
			if hasOffset {
				loc.Line = acp.Ptr(offset)
			} else {
				loc.Line = acp.Ptr(0)
			}
			locations = append(locations, loc)
		}
		return ToolInfo{Title: "Read File", Kind: acp.ToolKindRead, Locations: locations}
	case "LS":
		path := inputStr(input, "path")
		title := "List the "
		if path != "" {
			title += "`" + path + "`"
		} else {
			title += "current"
		}
		title += " directory's contents"
		return ToolInfo{Title: title, Kind: acp.ToolKindSearch}

	case ACPToolNamePrefix + "Edit", "Edit":
		filePath := inputStr(input, "file_path")
		title := "Edit"
		if filePath != "" {
			title = "Edit `" + filePath + "`"
		}
		var content []acp.ToolCallContent
		if filePath != "" {
			newStr := inputStr(input, "new_string")
			if _, hasOld := input["old_string"]; hasOld {
				oldStr := inputStr(input, "old_string")
				content = append(content, acp.ToolDiffContent(filePath, newStr, oldStr))
			} else {
				content = append(content, acp.ToolDiffContent(filePath, newStr))
			}
		}
		var locations []acp.ToolCallLocation
		if filePath != "" {
			locations = append(locations, acp.ToolCallLocation{Path: filePath})
		}
		return ToolInfo{Title: title, Kind: acp.ToolKindEdit, Content: content, Locations: locations}

	case ACPToolNamePrefix + "Write":
		filePath := inputStr(input, "file_path")
		fileContent := inputStr(input, "content")
		title := "Write"
		if filePath != "" {
			title = "Write " + filePath
		}
		var content []acp.ToolCallContent
		if filePath != "" {
			content = append(content, acp.ToolDiffContent(filePath, fileContent))
		} else if fileContent != "" {
			content = append(content, acp.ToolContent(acp.TextBlock(fileContent)))
		}
		var locations []acp.ToolCallLocation
		if filePath != "" {
			locations = append(locations, acp.ToolCallLocation{Path: filePath})
		}
		return ToolInfo{Title: title, Kind: acp.ToolKindEdit, Content: content, Locations: locations}
	case "Write":
		filePath := inputStr(input, "file_path")
		fileContent := inputStr(input, "content")
		title := "Write"
		if filePath != "" {
			title = "Write " + filePath
		}
		var content []acp.ToolCallContent
		if filePath != "" {
			content = append(content, acp.ToolDiffContent(filePath, fileContent))
		}
		var locations []acp.ToolCallLocation
		if filePath != "" {
			locations = append(locations, acp.ToolCallLocation{Path: filePath})
		}
		return ToolInfo{Title: title, Kind: acp.ToolKindEdit, Content: content, Locations: locations}

	case "Glob":
		label := "Find"
		if p := inputStr(input, "path"); p != "" {
			label += " `" + p + "`"
		}
		if pat := inputStr(input, "pattern"); pat != "" {
			label += " `" + pat + "`"
		}
		var locations []acp.ToolCallLocation
		if p := inputStr(input, "path"); p != "" {
			locations = append(locations, acp.ToolCallLocation{Path: p})
		}
		return ToolInfo{Title: label, Kind: acp.ToolKindSearch, Locations: locations}

	case "Grep":
		label := "grep"
		if inputBool(input, "-i") {
			label += " -i"
		}
		if inputBool(input, "-n") {
			label += " -n"
		}
		if a, ok := inputInt(input, "-A"); ok {
			label += fmt.Sprintf(" -A %d", a)
		}
		if b, ok := inputInt(input, "-B"); ok {
			label += fmt.Sprintf(" -B %d", b)
		}
		if c, ok := inputInt(input, "-C"); ok {
			label += fmt.Sprintf(" -C %d", c)
		}
		if om := inputStr(input, "output_mode"); om != "" {
			switch om {
			case "FilesWithMatches":
				label += " -l"
			case "Count":
				label += " -c"
			}
		}
		if hl, ok := inputInt(input, "head_limit"); ok {
			label += fmt.Sprintf(" | head -%d", hl)
		}
		if g := inputStr(input, "glob"); g != "" {
			label += fmt.Sprintf(" --include=\"%s\"", g)
		}
		if t := inputStr(input, "type"); t != "" {
			label += fmt.Sprintf(" --type=%s", t)
		}
		if inputBool(input, "multiline") {
			label += " -P"
		}
		if pat := inputStr(input, "pattern"); pat != "" {
			label += fmt.Sprintf(" \"%s\"", pat)
		}
		if p := inputStr(input, "path"); p != "" {
			label += " " + p
		}
		return ToolInfo{Title: label, Kind: acp.ToolKindSearch}

	case "WebFetch":
		url := inputStr(input, "url")
		title := "Fetch"
		if url != "" {
			title = "Fetch " + url
		}
		var content []acp.ToolCallContent
		if p := inputStr(input, "prompt"); p != "" {
			content = append(content, acp.ToolContent(acp.TextBlock(p)))
		}
		return ToolInfo{Title: title, Kind: acp.ToolKindFetch, Content: content}

	case "WebSearch":
		query := inputStr(input, "query")
		label := fmt.Sprintf("\"%s\"", query)
		if domains := inputStrSlice(input, "allowed_domains"); len(domains) > 0 {
			label += " (allowed: " + strings.Join(domains, ", ") + ")"
		}
		if domains := inputStrSlice(input, "blocked_domains"); len(domains) > 0 {
			label += " (blocked: " + strings.Join(domains, ", ") + ")"
		}
		return ToolInfo{Title: label, Kind: acp.ToolKindFetch}

	case "TodoWrite":
		title := "Update TODOs"
		if todos, ok := input["todos"].([]any); ok {
			parts := make([]string, 0, len(todos))
			for _, t := range todos {
				if m, ok := t.(map[string]any); ok {
					if c := inputStr(m, "content"); c != "" {
						parts = append(parts, c)
					}
				}
			}
			if len(parts) > 0 {
				title = "Update TODOs: " + strings.Join(parts, ", ")
			}
		}
		return ToolInfo{Title: title, Kind: acp.ToolKindThink}
	case "ExitPlanMode":
		title := "Ready to code?"
		var content []acp.ToolCallContent
		if p := inputStr(input, "plan"); p != "" {
			content = append(content, acp.ToolContent(acp.TextBlock(p)))
		}
		return ToolInfo{Title: title, Kind: acp.ToolKindSwitchMode, Content: content}

	case "Other":
		var output string
		data, err := json.MarshalIndent(input, "", "  ")
		if err != nil {
			output = "{}"
		} else {
			output = string(data)
		}
		title := name
		if title == "" {
			title = "Unknown Tool"
		}
		return ToolInfo{
			Title:   title,
			Kind:    acp.ToolKindOther,
			Content: []acp.ToolCallContent{acp.ToolContent(acp.TextBlock("```json\n" + output + "```"))},
		}

	default:
		title := name
		if title == "" {
			title = "Unknown Tool"
		}
		return ToolInfo{Title: title, Kind: acp.ToolKindOther}
	}
}
// toAcpContentBlock converts a tool result content block to an ACP ContentBlock.
func toAcpContentBlock(content map[string]any, isError bool) acp.ContentBlock {
	wrapText := func(text string) acp.ContentBlock {
		if isError {
			return acp.TextBlock("```\n" + text + "\n```")
		}
		return acp.TextBlock(text)
	}

	contentType, _ := content["type"].(string)
	switch contentType {
	case "text":
		text, _ := content["text"].(string)
		return wrapText(text)
	case "image":
		source, _ := content["source"].(map[string]any)
		if source != nil {
			srcType, _ := source["type"].(string)
			if srcType == "base64" {
				data, _ := source["data"].(string)
				mediaType, _ := source["media_type"].(string)
				return acp.ImageBlock(data, mediaType)
			}
			if srcType == "url" {
				url, _ := source["url"].(string)
				return wrapText("[image: " + url + "]")
			}
		}
		return wrapText("[image: file reference]")
	case "tool_reference":
		toolName, _ := content["tool_name"].(string)
		return wrapText("Tool: " + toolName)
	case "web_search_result":
		title, _ := content["title"].(string)
		url, _ := content["url"].(string)
		return wrapText(title + " (" + url + ")")
	case "web_search_tool_result_error":
		code, _ := content["error_code"].(string)
		return wrapText("Error: " + code)
	case "web_fetch_result":
		url, _ := content["url"].(string)
		return wrapText("Fetched: " + url)
	case "web_fetch_tool_result_error":
		code, _ := content["error_code"].(string)
		return wrapText("Error: " + code)
	case "code_execution_result", "bash_code_execution_result":
		stdout, _ := content["stdout"].(string)
		stderr, _ := content["stderr"].(string)
		out := stdout
		if out == "" {
			out = stderr
		}
		return wrapText("Output: " + out)
	case "code_execution_tool_result_error", "bash_code_execution_tool_result_error":
		code, _ := content["error_code"].(string)
		return wrapText("Error: " + code)
	case "text_editor_code_execution_view_result":
		c, _ := content["content"].(string)
		return wrapText(c)
	case "text_editor_code_execution_create_result":
		isUpdate, _ := content["is_file_update"].(bool)
		if isUpdate {
			return wrapText("File updated")
		}
		return wrapText("File created")
	case "text_editor_code_execution_str_replace_result":
		if lines, ok := content["lines"].([]any); ok {
			strs := make([]string, 0, len(lines))
			for _, l := range lines {
				if s, ok := l.(string); ok {
					strs = append(strs, s)
				}
			}
			return wrapText(strings.Join(strs, "\n"))
		}
		return wrapText("")
	case "text_editor_code_execution_tool_result_error":
		code, _ := content["error_code"].(string)
		msg, _ := content["error_message"].(string)
		text := "Error: " + code
		if msg != "" {
			text += " - " + msg
		}
		return wrapText(text)
	case "tool_search_tool_search_result":
		if refs, ok := content["tool_references"].([]any); ok {
			names := make([]string, 0, len(refs))
			for _, r := range refs {
				if m, ok := r.(map[string]any); ok {
					if n, ok := m["tool_name"].(string); ok {
						names = append(names, n)
					}
				}
			}
			result := "none"
			if len(names) > 0 {
				result = strings.Join(names, ", ")
			}
			return wrapText("Tools found: " + result)
		}
		return wrapText("Tools found: none")
	case "tool_search_tool_result_error":
		code, _ := content["error_code"].(string)
		msg, _ := content["error_message"].(string)
		text := "Error: " + code
		if msg != "" {
			text += " - " + msg
		}
		return wrapText(text)
	default:
		data, err := json.Marshal(content)
		if err != nil {
			return wrapText("{}")
		}
		return wrapText(string(data))
	}
}

// toAcpContentUpdate converts tool result content to ACP ToolCallContent slice.
func toAcpContentUpdate(content any, isError bool) ToolUpdate {
	switch c := content.(type) {
	case []any:
		if len(c) == 0 {
			return ToolUpdate{}
		}
		result := make([]acp.ToolCallContent, 0, len(c))
		for _, item := range c {
			if m, ok := item.(map[string]any); ok {
				result = append(result, acp.ToolContent(toAcpContentBlock(m, isError)))
			}
		}
		if len(result) > 0 {
			return ToolUpdate{Content: result}
		}
		return ToolUpdate{}
	case map[string]any:
		if _, ok := c["type"]; ok {
			return ToolUpdate{
				Content: []acp.ToolCallContent{acp.ToolContent(toAcpContentBlock(c, isError))},
			}
		}
		return ToolUpdate{}
	case string:
		if c == "" {
			return ToolUpdate{}
		}
		text := c
		if isError {
			text = "```\n" + c + "\n```"
		}
		return ToolUpdate{
			Content: []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(text))},
		}
	default:
		return ToolUpdate{}
	}
}

// toolUpdateFromToolResult converts a tool result to an ACP ToolUpdate.
func toolUpdateFromToolResult(toolResult map[string]any, toolUse *ToolUseEntry) ToolUpdate {
	isError, _ := toolResult["is_error"].(bool)
	content := toolResult["content"]

	// If it's an error with content, only return errors.
	if isError {
		if arr, ok := content.([]any); ok && len(arr) > 0 {
			return toAcpContentUpdate(content, true)
		}
		if s, ok := content.(string); ok && s != "" {
			return toAcpContentUpdate(content, true)
		}
	}

	toolName := ""
	if toolUse != nil {
		toolName = toolUse.Name
	}

	switch toolName {
	case "Read", ACPToolNames.Read:
		if arr, ok := content.([]any); ok && len(arr) > 0 {
			result := make([]acp.ToolCallContent, 0, len(arr))
			for _, item := range arr {
				if m, ok := item.(map[string]any); ok {
					if m["type"] == "text" {
						text, _ := m["text"].(string)
						text = strings.ReplaceAll(text, SystemReminder, "")
						result = append(result, acp.ToolContent(acp.TextBlock(markdownEscape(text))))
					} else {
						result = append(result, acp.ToolContent(toAcpContentBlock(m, false)))
					}
				}
			}
			if len(result) > 0 {
				return ToolUpdate{Content: result}
			}
		} else if s, ok := content.(string); ok && s != "" {
			s = strings.ReplaceAll(s, SystemReminder, "")
			return ToolUpdate{
				Content: []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(markdownEscape(s)))},
			}
		}
		return ToolUpdate{}
	case ACPToolNames.Edit:
		// Parse unified diff from the result content.
		var resultContent []acp.ToolCallContent
		var locations []acp.ToolCallLocation
		if arr, ok := content.([]any); ok && len(arr) > 0 {
			if first, ok := arr[0].(map[string]any); ok {
				if text, ok := first["text"].(string); ok {
					patches := parseUnifiedDiff(text)
					for _, p := range patches {
						for _, h := range p.hunks {
							var oldLines, newLines []string
							for _, line := range h.lines {
								if strings.HasPrefix(line, "-") {
									oldLines = append(oldLines, line[1:])
								} else if strings.HasPrefix(line, "+") {
									newLines = append(newLines, line[1:])
								} else if len(line) > 0 {
									oldLines = append(oldLines, line[1:])
									newLines = append(newLines, line[1:])
								}
							}
							if len(oldLines) > 0 || len(newLines) > 0 {
								fileName := p.newFileName
								if fileName == "" {
									fileName = p.oldFileName
								}
								locations = append(locations, acp.ToolCallLocation{
									Path: fileName,
									Line: acp.Ptr(h.newStart),
								})
								oldText := strings.Join(oldLines, "\n")
								newText := strings.Join(newLines, "\n")
								if oldText != "" {
									resultContent = append(resultContent, acp.ToolDiffContent(fileName, newText, oldText))
								} else {
									resultContent = append(resultContent, acp.ToolDiffContent(fileName, newText))
								}
							}
						}
					}
				}
			}
		}
		result := ToolUpdate{}
		if len(resultContent) > 0 {
			result.Content = resultContent
		}
		if len(locations) > 0 {
			result.Locations = locations
		}
		return result
	case ACPToolNames.Bash, "edit", "Edit", ACPToolNames.Write, "Write":
		return ToolUpdate{}

	case "ExitPlanMode":
		return ToolUpdate{Title: acp.Ptr("Exited Plan Mode")}

	default:
		return toAcpContentUpdate(content, isError)
	}
}

// diffPatch represents a parsed unified diff patch.
type toolsDiffPatch struct {
	oldFileName string
	newFileName string
	hunks       []toolsDiffHunk
}

// toolsDiffHunk represents a single hunk in a unified diff.
type toolsDiffHunk struct {
	newStart int
	lines    []string
}

// parseUnifiedDiff parses a unified diff string into patches.
func parseUnifiedDiff(text string) []toolsDiffPatch {
	lines := strings.Split(text, "\n")
	var patches []toolsDiffPatch
	var current *toolsDiffPatch
	var currentHunk *toolsDiffHunk

	for _, line := range lines {
		if strings.HasPrefix(line, "--- ") {
			if current != nil {
				if currentHunk != nil {
					current.hunks = append(current.hunks, *currentHunk)
					currentHunk = nil
				}
				patches = append(patches, *current)
			}
			current = &toolsDiffPatch{oldFileName: strings.TrimPrefix(line, "--- ")}
			currentHunk = nil
		} else if strings.HasPrefix(line, "+++ ") && current != nil {
			current.newFileName = strings.TrimPrefix(line, "+++ ")
		} else if strings.HasPrefix(line, "@@") && current != nil {
			if currentHunk != nil {
				current.hunks = append(current.hunks, *currentHunk)
			}
			newStart := parseHunkHeader(line)
			currentHunk = &toolsDiffHunk{newStart: newStart}
		} else if currentHunk != nil {
			if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, " ") {
				currentHunk.lines = append(currentHunk.lines, line)
			}
		}
	}
	if current != nil {
		if currentHunk != nil {
			current.hunks = append(current.hunks, *currentHunk)
		}
		patches = append(patches, *current)
	}
	return patches
}

// parseHunkHeader extracts the new start line from a @@ hunk header.
func parseHunkHeader(line string) int {
	// Format: @@ -old,count +new,count @@
	parts := strings.SplitN(line, "+", 2)
	if len(parts) < 2 {
		return 1
	}
	numStr := strings.SplitN(parts[1], ",", 2)[0]
	numStr = strings.SplitN(numStr, " ", 2)[0]
	n := 0
	for _, c := range numStr {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			break
		}
	}
	if n == 0 {
		return 1
	}
	return n
}

// planEntries converts Claude plan entries to ACP PlanEntry format.
func planEntries(todos []ClaudePlanEntry) []acp.PlanEntry {
	entries := make([]acp.PlanEntry, 0, len(todos))
	for _, t := range todos {
		status := acp.PlanEntryStatusPending
		switch t.Status {
		case "in_progress":
			status = acp.PlanEntryStatusInProgress
		case "completed":
			status = acp.PlanEntryStatusCompleted
		}
		entries = append(entries, acp.PlanEntry{
			Content:  t.Content,
			Status:   status,
			Priority: acp.PlanEntryPriorityMedium,
		})
	}
	return entries
}

// toAcpNotifications converts Claude messages to ACP SessionNotification slices.
// content can be a string or []any (array of content blocks from Claude SDK).
func toAcpNotifications(
	content any,
	role string,
	sessionID string,
	toolUseCache map[string]ToolUseEntry,
	parentToolCallID *string,
) []acp.SessionNotification {
	sid := acp.SessionId(sessionID)

	if text, ok := content.(string); ok {
		var update acp.SessionUpdate
		if role == "assistant" {
			update = acp.UpdateAgentMessageText(text)
		} else {
			update = acp.UpdateUserMessageText(text)
		}
		return []acp.SessionNotification{{SessionId: sid, Update: update}}
	}

	blocks, ok := content.([]any)
	if !ok {
		return nil
	}

	var output []acp.SessionNotification
	for _, block := range blocks {
		chunk, ok := block.(map[string]any)
		if !ok {
			continue
		}
		chunkType, _ := chunk["type"].(string)

		var notification *acp.SessionNotification
		switch chunkType {
		case "text", "text_delta":
			text, _ := chunk["text"].(string)
			var update acp.SessionUpdate
			if role == "assistant" {
				update = acp.UpdateAgentMessageText(text)
			} else {
				update = acp.UpdateUserMessageText(text)
			}
			notification = &acp.SessionNotification{SessionId: sid, Update: update}

		case "image":
			source, _ := chunk["source"].(map[string]any)
			if source != nil {
				srcType, _ := source["type"].(string)
				if srcType == "base64" {
					data, _ := source["data"].(string)
					mediaType, _ := source["media_type"].(string)
					var update acp.SessionUpdate
					if role == "assistant" {
						update = acp.UpdateAgentMessage(acp.ImageBlock(data, mediaType))
					} else {
						update = acp.UpdateUserMessage(acp.ImageBlock(data, mediaType))
					}
					notification = &acp.SessionNotification{SessionId: sid, Update: update}
				}
			}
		case "thinking", "thinking_delta":
			thinking, _ := chunk["thinking"].(string)
			update := acp.UpdateAgentThoughtText(thinking)
			notification = &acp.SessionNotification{SessionId: sid, Update: update}

		case "tool_use", "server_tool_use", "mcp_tool_use":
			id, _ := chunk["id"].(string)
			name, _ := chunk["name"].(string)
			inputRaw, _ := chunk["input"].(map[string]any)

			toolUseCache[id] = ToolUseEntry{
				Type:  chunkType,
				ID:    id,
				Name:  name,
				Input: inputRaw,
			}

			if name == "TodoWrite" {
				if inputRaw != nil {
					if todosRaw, ok := inputRaw["todos"].([]any); ok {
						var todos []ClaudePlanEntry
						for _, t := range todosRaw {
							if m, ok := t.(map[string]any); ok {
								todos = append(todos, ClaudePlanEntry{
									Content:    inputStr(m, "content"),
									Status:     inputStr(m, "status"),
									ActiveForm: inputStr(m, "activeForm"),
								})
							}
						}
						if len(todos) > 0 {
							update := acp.UpdatePlan(planEntries(todos)...)
							notification = &acp.SessionNotification{SessionId: sid, Update: update}
						}
					}
				}
			} else {
				info := toolInfoFromToolUse(name, inputRaw)
				meta := map[string]any{
					"claudeCode": map[string]any{
						"toolName":         name,
						"parentToolCallId": parentToolCallID,
					},
				}
				opts := []acp.ToolCallStartOpt{
					acp.WithStartKind(info.Kind),
					acp.WithStartStatus(acp.ToolCallStatusPending),
				}
				if len(info.Content) > 0 {
					opts = append(opts, acp.WithStartContent(info.Content))
				}
				if len(info.Locations) > 0 {
					opts = append(opts, acp.WithStartLocations(info.Locations))
				}
				if inputRaw != nil {
					opts = append(opts, acp.WithStartRawInput(inputRaw))
				}
				update := acp.StartToolCall(acp.ToolCallId(id), info.Title, opts...)
				if update.ToolCall != nil {
					update.ToolCall.Meta = meta
				}
				notification = &acp.SessionNotification{SessionId: sid, Update: update}
			}

		case "tool_result", "tool_search_tool_result", "web_fetch_tool_result",
			"web_search_tool_result", "code_execution_tool_result",
			"bash_code_execution_tool_result", "text_editor_code_execution_tool_result",
			"mcp_tool_result":
			toolUseID, _ := chunk["tool_use_id"].(string)
			cachedToolUse, exists := toolUseCache[toolUseID]
			if !exists {
				continue
			}
			if cachedToolUse.Name == "TodoWrite" {
				continue
			}

			isErr, _ := chunk["is_error"].(bool)
			status := acp.ToolCallStatusCompleted
			if isErr {
				status = acp.ToolCallStatusFailed
			}

			toolResultMap := chunk
			tu := toolUpdateFromToolResult(toolResultMap, &cachedToolUse)

			meta := map[string]any{
				"claudeCode": map[string]any{
					"toolName":         cachedToolUse.Name,
					"parentToolCallId": parentToolCallID,
				},
			}

			updateOpts := []acp.ToolCallUpdateOpt{
				acp.WithUpdateStatus(status),
				acp.WithUpdateRawOutput(chunk["content"]),
			}
			if tu.Title != nil {
				updateOpts = append(updateOpts, acp.WithUpdateTitle(*tu.Title))
			}
			if len(tu.Content) > 0 {
				updateOpts = append(updateOpts, acp.WithUpdateContent(tu.Content))
			}
			if len(tu.Locations) > 0 {
				updateOpts = append(updateOpts, acp.WithUpdateLocations(tu.Locations))
			}
			update := acp.UpdateToolCall(acp.ToolCallId(toolUseID), updateOpts...)
			if update.ToolCallUpdate != nil {
				update.ToolCallUpdate.Meta = meta
			}
			notification = &acp.SessionNotification{SessionId: sid, Update: update}
		case "document", "search_result", "redacted_thinking",
			"input_json_delta", "citations_delta", "signature_delta",
			"container_upload", "compaction", "compaction_delta":
			// Ignored block types.
			continue

		default:
			continue
		}

		if notification != nil {
			output = append(output, *notification)
		}
	}

	return output
}

// streamEventToAcpNotifications converts Claude stream events to ACP notifications.
func streamEventToAcpNotifications(
	msg map[string]any,
	sessionID string,
	toolUseCache map[string]ToolUseEntry,
	parentToolCallID *string,
) []acp.SessionNotification {
	event, _ := msg["event"].(map[string]any)
	if event == nil {
		return nil
	}
	eventType, _ := event["type"].(string)

	switch eventType {
	case "content_block_start":
		contentBlock, _ := event["content_block"].(map[string]any)
		if contentBlock == nil {
			return nil
		}
		return toAcpNotifications(
			[]any{contentBlock},
			"assistant",
			sessionID,
			toolUseCache,
			parentToolCallID,
		)

	case "content_block_delta":
		delta, _ := event["delta"].(map[string]any)
		if delta == nil {
			return nil
		}
		return toAcpNotifications(
			[]any{delta},
			"assistant",
			sessionID,
			toolUseCache,
			parentToolCallID,
		)

	case "message_start", "message_delta", "message_stop", "content_block_stop":
		return nil

	default:
		return nil
	}
}
