package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

const MaxFileSize = 50000
const LinesToRead = 2000

// EditOperation represents a single text replacement operation.
type EditOperation struct {
	OldText    string
	NewText    string
	ReplaceAll bool
}

// handleBuiltinTool handles a built-in tool call.
// toolName should be the unqualified name (without the mcp__acp__ prefix).
func handleBuiltinTool(
	ctx context.Context,
	conn *acp.AgentSideConnection,
	sessionID string,
	toolName string,
	input map[string]any,
) (string, bool, error) {
	switch toolName {
	case "Read":
		return handleRead(ctx, conn, sessionID, input)
	case "Write":
		return handleWrite(ctx, conn, sessionID, input)
	case "Edit":
		return handleEdit(ctx, conn, sessionID, input)
	case "Bash":
		return handleBash(ctx, conn, sessionID, input)
	case "BashOutput":
		return handleBashOutput(ctx, conn, sessionID, input)
	case "KillShell":
		return handleKillShell(ctx, conn, sessionID, input)
	default:
		return fmt.Sprintf("Unknown tool: %s", toolName), true, nil
	}
}

func handleRead(ctx context.Context, conn *acp.AgentSideConnection, sessionID string, input map[string]any) (string, bool, error) {
	filePath := inputStr(input, "file_path")
	if filePath == "" {
		return "file_path is required", true, nil
	}

	var rawContent string
	if isInternalPath(filePath) {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "Reading file failed: " + err.Error(), true, nil
		}
		content := string(data)
		offset, hasOffset := inputInt(input, "offset")
		limit, hasLimit := inputInt(input, "limit")
		if hasOffset || hasLimit {
			lines := strings.Split(content, "\n")
			start := 0
			if hasOffset {
				start = offset - 1
			}
			if start < 0 {
				start = 0
			}
			end := len(lines)
			if hasLimit {
				end = start + limit
			}
			if end > len(lines) {
				end = len(lines)
			}
			content = strings.Join(lines[start:end], "\n")
		}
		rawContent = content
	} else {
		resp, err := conn.ReadTextFile(ctx, acp.ReadTextFileRequest{
			SessionId: acp.SessionId(sessionID),
			Path:      filePath,
		})
		if err != nil {
			return "Reading file failed: " + err.Error(), true, nil
		}
		rawContent = resp.Content
	}

	offset, hasOffset := inputInt(input, "offset")
	result := extractLinesWithByteLimit(rawContent, MaxFileSize)
	var readInfo string
	if (hasOffset && offset > 1) || result.WasLimited {
		readInfo = "\n\n<file-read-info>"
		if result.WasLimited {
			readInfo += fmt.Sprintf("Read %d lines (hit 50KB limit). ", result.LinesRead)
			readInfo += fmt.Sprintf("Continue with offset=%d.", result.LinesRead)
		} else if hasOffset && offset > 1 {
			readInfo += fmt.Sprintf("Read lines %d-%d.", offset, offset+result.LinesRead)
		}
		readInfo += "</file-read-info>"
	}
	return result.Content + readInfo + SystemReminder, false, nil
}

func handleWrite(ctx context.Context, conn *acp.AgentSideConnection, sessionID string, input map[string]any) (string, bool, error) {
	filePath := inputStr(input, "file_path")
	if filePath == "" {
		return "file_path is required", true, nil
	}
	content := inputStr(input, "content")
	if isInternalPath(filePath) {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			return "Writing file failed: " + err.Error(), true, nil
		}
		if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
			return "Writing file failed: " + err.Error(), true, nil
		}
		return fmt.Sprintf("The file %s has been updated successfully.", filePath), false, nil
	}
	_, err := conn.WriteTextFile(ctx, acp.WriteTextFileRequest{
		SessionId: acp.SessionId(sessionID),
		Path:      filePath,
		Content:   content,
	})
	if err != nil {
		return "Writing file failed: " + err.Error(), true, nil
	}
	return fmt.Sprintf("The file %s has been updated successfully.", filePath), false, nil
}

func handleEdit(ctx context.Context, conn *acp.AgentSideConnection, sessionID string, input map[string]any) (string, bool, error) {
	filePath := inputStr(input, "file_path")
	if filePath == "" {
		return "file_path is required", true, nil
	}
	oldString := inputStr(input, "old_string")
	newString := inputStr(input, "new_string")
	replaceAll := inputBool(input, "replace_all")

	var fileContent string
	if isInternalPath(filePath) {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "Editing file failed: " + err.Error(), true, nil
		}
		fileContent = string(data)
	} else {
		resp, err := conn.ReadTextFile(ctx, acp.ReadTextFileRequest{
			SessionId: acp.SessionId(sessionID),
			Path:      filePath,
		})
		if err != nil {
			return "Editing file failed: " + err.Error(), true, nil
		}
		fileContent = resp.Content
	}
	newContent, _, err := replaceAndCalculateLocation(fileContent, []EditOperation{
		{OldText: oldString, NewText: newString, ReplaceAll: replaceAll},
	})
	if err != nil {
		return "Editing file failed: " + err.Error(), true, nil
	}
	patch := createUnifiedDiff(filePath, fileContent, newContent)
	if isInternalPath(filePath) {
		if err := os.WriteFile(filePath, []byte(newContent), 0o644); err != nil {
			return "Editing file failed: " + err.Error(), true, nil
		}
	} else {
		_, err := conn.WriteTextFile(ctx, acp.WriteTextFileRequest{
			SessionId: acp.SessionId(sessionID),
			Path:      filePath,
			Content:   newContent,
		})
		if err != nil {
			return "Editing file failed: " + err.Error(), true, nil
		}
	}
	return patch, false, nil
}

func handleBash(ctx context.Context, conn *acp.AgentSideConnection, sessionID string, input map[string]any) (string, bool, error) {
	command := inputStr(input, "command")
	if command == "" {
		return "command is required", true, nil
	}
	timeoutMs := 2 * 60 * 1000
	if t, ok := inputInt(input, "timeout"); ok {
		timeoutMs = t
	}
	runInBackground := inputBool(input, "run_in_background")
	outputByteLimit := 32000
	resp, err := conn.CreateTerminal(ctx, acp.CreateTerminalRequest{
		Command:         command,
		Env:             []acp.EnvVariable{{Name: "CLAUDECODE", Value: "1"}},
		SessionId:       acp.SessionId(sessionID),
		OutputByteLimit: &outputByteLimit,
	})
	if err != nil {
		return "Running bash command failed: " + err.Error(), true, nil
	}
	terminalID := resp.TerminalId
	if runInBackground {
		return fmt.Sprintf("Command started in background with id: %s", terminalID), false, nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	exitResp, err := conn.WaitForTerminalExit(waitCtx, acp.WaitForTerminalExitRequest{
		SessionId:  acp.SessionId(sessionID),
		TerminalId: terminalID,
	})
	var status string
	var exitCode *int
	var signal string
	if err != nil {
		if waitCtx.Err() != nil {
			_, _ = conn.KillTerminalCommand(ctx, acp.KillTerminalCommandRequest{
				SessionId:  acp.SessionId(sessionID),
				TerminalId: terminalID,
			})
			status = "timedOut"
		} else {
			status = "exited"
		}
	} else {
		status = "exited"
		exitCode = exitResp.ExitCode
		if exitResp.Signal != nil {
			signal = *exitResp.Signal
		}
	}
	outputResp, outputErr := conn.TerminalOutput(ctx, acp.TerminalOutputRequest{
		SessionId:  acp.SessionId(sessionID),
		TerminalId: terminalID,
	})
	var output string
	var truncated bool
	if outputErr == nil {
		output = outputResp.Output
		truncated = outputResp.Truncated
	}
	_, _ = conn.ReleaseTerminal(ctx, acp.ReleaseTerminalRequest{
		SessionId:  acp.SessionId(sessionID),
		TerminalId: terminalID,
	})
	return formatToolCommandOutput(status, output, exitCode, signal, truncated), false, nil
}

func handleBashOutput(ctx context.Context, conn *acp.AgentSideConnection, sessionID string, input map[string]any) (string, bool, error) {
	taskID := inputStr(input, "task_id")
	if taskID == "" {
		return "task_id is required", true, nil
	}
	block := inputBool(input, "block")
	timeoutMs := 2 * 60 * 1000
	if t, ok := inputInt(input, "timeout"); ok {
		timeoutMs = t
	}
	if block {
		waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
		defer cancel()
		exitResp, err := conn.WaitForTerminalExit(waitCtx, acp.WaitForTerminalExitRequest{
			SessionId:  acp.SessionId(sessionID),
			TerminalId: taskID,
		})
		var status string
		var exitCode *int
		var signal string
		if err != nil {
			if waitCtx.Err() != nil {
				_, _ = conn.KillTerminalCommand(ctx, acp.KillTerminalCommandRequest{
					SessionId:  acp.SessionId(sessionID),
					TerminalId: taskID,
				})
				status = "timedOut"
			} else {
				status = "exited"
			}
		} else {
			status = "exited"
			exitCode = exitResp.ExitCode
			if exitResp.Signal != nil {
				signal = *exitResp.Signal
			}
		}
		outputResp, outputErr := conn.TerminalOutput(ctx, acp.TerminalOutputRequest{
			SessionId:  acp.SessionId(sessionID),
			TerminalId: taskID,
		})
		var output string
		var truncated bool
		if outputErr == nil {
			output = outputResp.Output
			truncated = outputResp.Truncated
		}
		_, _ = conn.ReleaseTerminal(ctx, acp.ReleaseTerminalRequest{
			SessionId:  acp.SessionId(sessionID),
			TerminalId: taskID,
		})
		return formatToolCommandOutput(status, output, exitCode, signal, truncated), false, nil
	}
	outputResp, err := conn.TerminalOutput(ctx, acp.TerminalOutputRequest{
		SessionId:  acp.SessionId(sessionID),
		TerminalId: taskID,
	})
	if err != nil {
		return "Retrieving bash output failed: " + err.Error(), true, nil
	}
	return formatToolCommandOutput("started", outputResp.Output, nil, "", outputResp.Truncated), false, nil
}

func handleKillShell(ctx context.Context, conn *acp.AgentSideConnection, sessionID string, input map[string]any) (string, bool, error) {
	shellID := inputStr(input, "shell_id")
	if shellID == "" {
		return "shell_id is required", true, nil
	}
	_, err := conn.KillTerminalCommand(ctx, acp.KillTerminalCommandRequest{
		SessionId:  acp.SessionId(sessionID),
		TerminalId: shellID,
	})
	if err != nil {
		return "Killing shell failed: " + err.Error(), true, nil
	}
	return "Command killed successfully.", false, nil
}

// replaceAndCalculateLocation performs text replacements and tracks line numbers
// where replacements occur. Returns the new content and sorted unique line numbers.
func replaceAndCalculateLocation(fileContent string, edits []EditOperation) (string, []int, error) {
	currentContent := fileContent
	markerPrefix := fmt.Sprintf("__REPLACE_MARKER_%s_", randomString(9))
	markerCounter := 0
	var markers []string

	for _, edit := range edits {
		if edit.OldText == "" {
			return "", nil, fmt.Errorf("The provided `old_string` is empty.\n\nNo edits were applied.")
		}
		if edit.ReplaceAll {
			var parts []string
			lastIndex := 0
			searchIndex := 0
			found := false
			for {
				idx := strings.Index(currentContent[searchIndex:], edit.OldText)
				if idx == -1 {
					if !found {
						return "", nil, fmt.Errorf(
							"The provided `old_string` does not appear in the file: %q.\n\nNo edits were applied.",
							edit.OldText,
						)
					}
					break
				}
				found = true
				idx += searchIndex
				parts = append(parts, currentContent[lastIndex:idx])
				marker := fmt.Sprintf("%s%d__", markerPrefix, markerCounter)
				markerCounter++
				markers = append(markers, marker)
				parts = append(parts, marker+edit.NewText)
				lastIndex = idx + len(edit.OldText)
				searchIndex = lastIndex
			}
			parts = append(parts, currentContent[lastIndex:])
			currentContent = strings.Join(parts, "")
		} else {
			idx := strings.Index(currentContent, edit.OldText)
			if idx == -1 {
				return "", nil, fmt.Errorf(
					"The provided `old_string` does not appear in the file: %q.\n\nNo edits were applied.",
					edit.OldText,
				)
			}

			marker := fmt.Sprintf("%s%d__", markerPrefix, markerCounter)
			markerCounter++
			markers = append(markers, marker)
			currentContent = currentContent[:idx] + marker + edit.NewText + currentContent[idx+len(edit.OldText):]
		}
	}

	// Find line numbers where markers appear
	var lineNumbers []int
	for _, marker := range markers {
		idx := strings.Index(currentContent, marker)
		if idx != -1 {
			lineNum := countLines(currentContent[:idx])
			lineNumbers = append(lineNumbers, lineNum)
		}
	}

	// Remove all markers from the final content
	finalContent := currentContent
	for _, marker := range markers {
		finalContent = strings.Replace(finalContent, marker, "", 1)
	}

	// Dedupe and sort line numbers
	seen := make(map[int]bool)
	var unique []int
	for _, ln := range lineNumbers {
		if !seen[ln] {
			seen[ln] = true
			unique = append(unique, ln)
		}
	}
	sort.Ints(unique)

	return finalContent, unique, nil
}

type diffHunk struct {
	oldStart int
	oldCount int
	newStart int
	newCount int
	lines    []string
}

// createUnifiedDiff creates a unified diff patch between old and new content.
func createUnifiedDiff(filename, oldContent, newContent string) string {
	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)
	hunks := computeDiffHunks(oldLines, newLines)
	if len(hunks) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("--- a/" + filename + "\n")
	sb.WriteString("+++ b/" + filename + "\n")
	for _, hunk := range hunks {
		sb.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n",
			hunk.oldStart+1, hunk.oldCount,
			hunk.newStart+1, hunk.newCount))
		for _, line := range hunk.lines {
			sb.WriteString(line + "\n")
		}
	}
	return sb.String()
}

// splitLines splits content into lines.
func splitLines(content string) []string {
	if content == "" {
		return []string{}
	}
	return strings.Split(content, "\n")
}

// computeDiffHunks computes unified diff hunks between old and new line slices.
func computeDiffHunks(oldLines, newLines []string) []diffHunk {
	m := len(oldLines)
	n := len(newLines)

	// Build LCS table
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] > lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	type diffOp struct {
		op   byte
		line string
		oldN int
		newN int
	}

	var ops []diffOp
	i, j := 0, 0
	for i < m && j < n {
		if oldLines[i] == newLines[j] {
			ops = append(ops, diffOp{' ', oldLines[i], i, j})
			i++
			j++
		} else if lcs[i+1][j] >= lcs[i][j+1] {
			ops = append(ops, diffOp{'-', oldLines[i], i, j})
			i++
		} else {
			ops = append(ops, diffOp{'+', newLines[j], i, j})
			j++
		}
	}
	for i < m {
		ops = append(ops, diffOp{'-', oldLines[i], i, j})
		i++
	}
	for j < n {
		ops = append(ops, diffOp{'+', newLines[j], i, j})
		j++
	}

	const contextLines = 3
	var hunks []diffHunk
	var currentHunk *diffHunk

	for idx, op := range ops {
		if op.op != ' ' {
			if currentHunk == nil {
				currentHunk = &diffHunk{}
				start := idx - contextLines
				if start < 0 {
					start = 0
				}
				for ci := start; ci < idx; ci++ {
					if ops[ci].op == ' ' {
						currentHunk.lines = append(currentHunk.lines, " "+ops[ci].line)
						currentHunk.oldCount++
						currentHunk.newCount++
						if currentHunk.oldCount == 1 && currentHunk.newCount == 1 {
							currentHunk.oldStart = ops[ci].oldN
							currentHunk.newStart = ops[ci].newN
						}
					}
				}
				if currentHunk.oldCount == 0 && currentHunk.newCount == 0 {
					currentHunk.oldStart = op.oldN
					currentHunk.newStart = op.newN
				}
			}
			currentHunk.lines = append(currentHunk.lines, string(op.op)+op.line)
			if op.op == '-' {
				currentHunk.oldCount++
			} else {
				currentHunk.newCount++
			}
		} else if currentHunk != nil {
			nextChange := -1
			limit := idx + 2*contextLines + 1
			if limit > len(ops) {
				limit = len(ops)
			}
			for ni := idx + 1; ni < limit; ni++ {
				if ops[ni].op != ' ' {
					nextChange = ni
					break
				}
			}

			if nextChange != -1 {
				currentHunk.lines = append(currentHunk.lines, " "+op.line)
				currentHunk.oldCount++
				currentHunk.newCount++
			} else {
				trailEnd := idx + contextLines
				if trailEnd >= len(ops) {
					trailEnd = len(ops) - 1
				}
				for ci := idx; ci <= trailEnd; ci++ {
					if ops[ci].op == ' ' {
						currentHunk.lines = append(currentHunk.lines, " "+ops[ci].line)
						currentHunk.oldCount++
						currentHunk.newCount++
					}
				}
				hunks = append(hunks, *currentHunk)
				currentHunk = nil
			}
		}
	}
	if currentHunk != nil {
		hunks = append(hunks, *currentHunk)
	}
	return hunks
}

// stripCommonPrefix removes the common prefix of b relative to a.
func stripCommonPrefix(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return b[i:]
}

// formatToolCommandOutput formats terminal output for display.
func formatToolCommandOutput(status string, output string, exitCode *int, signal string, truncated bool) string {
	var sb strings.Builder
	switch status {
	case "started", "exited":
		if exitCode == nil && signal == "" {
			sb.WriteString("Interrupted by the user. ")
		}
	case "killed":
		sb.WriteString("Killed. ")
	case "timedOut":
		sb.WriteString("Timed out. ")
	}
	if exitCode != nil {
		sb.WriteString(fmt.Sprintf("Exited with code %d.", *exitCode))
	}
	if signal != "" {
		sb.WriteString(fmt.Sprintf("Signal `%s`. ", signal))
	}
	if exitCode != nil || signal != "" {
		sb.WriteString("Final output:\n\n")
	} else {
		sb.WriteString("New output:\n\n")
	}
	sb.WriteString(output)
	if truncated {
		sb.WriteString(fmt.Sprintf("\n\nCommand output was too long, so it was truncated to %d bytes.", len(output)))
	}
	return sb.String()
}

// isInternalPath checks if a path is in ~/.claude/ but not settings.json or session-env.
func isInternalPath(filePath string) bool {
	claudeDir := getClaudeConfigDir()
	if !strings.HasPrefix(filePath, claudeDir) {
		return false
	}
	if strings.HasPrefix(filePath, filepath.Join(claudeDir, "settings.json")) {
		return false
	}
	if strings.HasPrefix(filePath, filepath.Join(claudeDir, "session-env")) {
		return false
	}
	return true
}

// randomString generates a random alphanumeric string of the given length.
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

// countLines counts the number of line breaks in text,
// handling \r\n, \r, and \n line endings (matching TS split(/\r\n|\r|\n/) behavior).
func countLines(text string) int {
	count := 0
	i := 0
	for i < len(text) {
		if text[i] == '\r' {
			count++
			if i+1 < len(text) && text[i+1] == '\n' {
				i += 2
			} else {
				i++
			}
		} else if text[i] == '\n' {
			count++
			i++
		} else {
			i++
		}
	}
	return count
}
