package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// ClaudeCodeOptions configures the Claude Code subprocess
type ClaudeCodeOptions struct {
	Cwd            string
	SessionID      string
	PermissionMode string // "default"|"acceptEdits"|"bypassPermissions"|"dontAsk"|"plan"
	McpServers     map[string]McpServerConfig
	SystemPrompt   string
	Resume            string // optional session ID to resume
	Executable        string // claude CLI path, defaults to "claude"
	MaxTurns          int
	MaxThinkingTokens int // 0 means not set
}

type McpServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Type    string            `json:"type,omitempty"` // "stdio"|"sse"|"http"
}

// SDKMessage represents a message in the Claude Code SDK protocol
type SDKMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []ContentBlock
}

// SDKUserMessage is sent to Claude Code subprocess
type SDKUserMessage struct {
	Type            string     `json:"type"` // always "user"
	Message         SDKMessage `json:"message"`
	SessionID       string     `json:"session_id"`
	ParentToolUseID *string    `json:"parent_tool_use_id,omitempty"`
}

// SDKResponse is a line from Claude Code subprocess stdout (ndjson)
type SDKResponse struct {
	Type      string          `json:"type"`              // system|result|assistant|user|stream_event
	Subtype   string          `json:"subtype,omitempty"` // for result: success|error_max_turns|error_*
	SessionID string          `json:"session_id,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	Error     *SDKError       `json:"error,omitempty"`
	Errors    []string        `json:"errors,omitempty"`  // For result type error messages
	IsError   bool            `json:"is_error,omitempty"` // For result type
	Result    string          `json:"result,omitempty"`  // For result type success message
	Tools     json.RawMessage `json:"tools,omitempty"`
	Model     string          `json:"model,omitempty"`
	Event     json.RawMessage `json:"event,omitempty"` // For stream_event type
	RawLine   json.RawMessage `json:"-"`               // Original ndjson line, preserved for lossless field access
}

type SDKError struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// SDKContentBlock represents a content block in Claude's response
type SDKContentBlock struct {
	Type     string          `json:"type"` // text|tool_use|tool_result|thinking
	Text     string          `json:"text,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	Content  interface{}     `json:"content,omitempty"` // for tool_result
	IsError  *bool           `json:"is_error,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
}

// StreamEvent represents a streaming event from Claude Code
type StreamEvent struct {
	Type         string           `json:"type"` // content_block_start|content_block_delta|content_block_stop|message_start|message_delta|message_stop
	Index        int              `json:"index,omitempty"`
	ContentBlock *SDKContentBlock `json:"content_block,omitempty"`
	Delta        json.RawMessage  `json:"delta,omitempty"`
}

// ClaudeCodeProcess manages communication with the Claude Code CLI subprocess
type ClaudeCodeProcess struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	done    chan struct{}
	mu      sync.Mutex
}

// NewClaudeCodeProcess starts a Claude Code subprocess with the given options.
func NewClaudeCodeProcess(opts ClaudeCodeOptions) (*ClaudeCodeProcess, error) {
	executable := opts.Executable
	if executable == "" {
		executable = "claude"
	}

	maxTurns := opts.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 200
	}

	args := []string{
		"--input-format=stream-json",
		"--output-format=stream-json",
		"--verbose",
		"--include-partial-messages",
		fmt.Sprintf("--max-turns=%d", maxTurns),
		fmt.Sprintf("--session-id=%s", opts.SessionID),
	}

	if opts.PermissionMode != "" {
		args = append(args, fmt.Sprintf("--permission-mode=%s", opts.PermissionMode))
	}

	if opts.Resume != "" {
		args = append(args, "--resume")
	}

	if opts.SystemPrompt != "" {
		args = append(args, fmt.Sprintf("--system-prompt=%s", opts.SystemPrompt))
	}

	if opts.MaxThinkingTokens > 0 {
		args = append(args, fmt.Sprintf("--max-thinking-tokens=%d", opts.MaxThinkingTokens))
	}

	if len(opts.McpServers) > 0 {
		tmpFile, err := os.CreateTemp("", "mcp-config-*.json")
		if err != nil {
			return nil, fmt.Errorf("failed to create mcp config temp file: %w", err)
		}
		mcpConfig := map[string]interface{}{
			"mcpServers": opts.McpServers,
		}
		if err := json.NewEncoder(tmpFile).Encode(mcpConfig); err != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			return nil, fmt.Errorf("failed to write mcp config: %w", err)
		}
		tmpFile.Close()
		args = append(args, fmt.Sprintf("--mcp-config=%s", tmpFile.Name()))
	}

	cmd := exec.Command(executable, args...)
	cmd.Dir = opts.Cwd
	cmd.Stderr = os.Stderr

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start claude process: %w", err)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 10*1024*1024), 10*1024*1024) // 10MB buffer

	p := &ClaudeCodeProcess{
		cmd:     cmd,
		stdin:   stdinPipe,
		scanner: scanner,
		done:    make(chan struct{}),
	}

	return p, nil
}

// SendMessage sends a user message to the Claude Code subprocess via stdin.
func (p *ClaudeCodeProcess) SendMessage(msg SDKUserMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	data = append(data, '\n')
	if _, err := p.stdin.Write(data); err != nil {
		return fmt.Errorf("failed to write to stdin: %w", err)
	}

	return nil
}

// ReadMessage reads the next ndjson line from the subprocess stdout.
// Returns nil, io.EOF when there are no more lines.
func (p *ClaudeCodeProcess) ReadMessage() (*SDKResponse, error) {
	if !p.scanner.Scan() {
		if err := p.scanner.Err(); err != nil {
			return nil, fmt.Errorf("scanner error: %w", err)
		}
		return nil, io.EOF
	}

	line := p.scanner.Bytes()
	// Make a copy since scanner reuses the buffer
	rawCopy := make([]byte, len(line))
	copy(rawCopy, line)

	var resp SDKResponse
	if err := json.Unmarshal(rawCopy, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	resp.RawLine = rawCopy

	return &resp, nil
}

// Close shuts down the subprocess by closing stdin and waiting for exit.
func (p *ClaudeCodeProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.stdin.Close(); err != nil {
		return fmt.Errorf("failed to close stdin: %w", err)
	}

	err := p.cmd.Wait()
	close(p.done)
	return err
}

// Done returns a channel that is closed when the process exits.
func (p *ClaudeCodeProcess) Done() <-chan struct{} {
	return p.done
}
