package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"
)

// ClaudeAcpAgent implements the acp.Agent interface, bridging ACP protocol
// requests to the Claude Code CLI subprocess.
type ClaudeAcpAgent struct {
	conn               *acp.AgentSideConnection
	sessions           map[string]*Session
	mu                 sync.RWMutex
	toolUseCache       map[string]ToolUseEntry
	clientCapabilities *acp.ClientCapabilities
	logger             *slog.Logger
	allowBypass        bool
}

// Compile-time interface checks.
var _ acp.Agent = (*ClaudeAcpAgent)(nil)

// NewClaudeAcpAgent creates a new ClaudeAcpAgent.
func NewClaudeAcpAgent(logger *slog.Logger) *ClaudeAcpAgent {
	allowBypass := true
	if isRootUser() && os.Getenv("IS_SANDBOX") == "" {
		allowBypass = false
	}
	return &ClaudeAcpAgent{
		sessions:     make(map[string]*Session),
		toolUseCache: make(map[string]ToolUseEntry),
		logger:       logger,
		allowBypass:  allowBypass,
	}
}

// SetAgentConnection stores the ACP connection for sending notifications.
func (a *ClaudeAcpAgent) SetAgentConnection(conn *acp.AgentSideConnection) {
	a.conn = conn
}

// validModes are the session modes supported by this agent.
var validModes = []acp.SessionMode{
	{Id: "default", Name: "Default", Description: acp.Ptr("Normal operation with permission prompts")},
	{Id: "acceptEdits", Name: "Accept Edits", Description: acp.Ptr("Automatically accept file edits")},
	{Id: "plan", Name: "Plan", Description: acp.Ptr("Plan-only mode, no execution")},
	{Id: "dontAsk", Name: "Don't Ask", Description: acp.Ptr("Skip permission prompts for allowed tools")},
	{Id: "bypassPermissions", Name: "Bypass Permissions", Description: acp.Ptr("Skip all permission prompts")},
}

// Initialize handles the ACP initialize handshake.
func (a *ClaudeAcpAgent) Initialize(ctx context.Context, params acp.InitializeRequest) (acp.InitializeResponse, error) {
	caps := params.ClientCapabilities
	a.clientCapabilities = &caps

	authMethod := acp.AuthMethod{
		Id:          "claude-login",
		Name:        "Log in with Claude Code",
		Description: acp.Ptr("Run `claude /login` in the terminal"),
	}
	if caps.Meta != nil {
		if meta, ok := caps.Meta.(map[string]any); ok {
			if v, ok := meta["terminal-auth"]; ok {
				if enabled, ok := v.(bool); ok && enabled {
					authMethod.Meta = map[string]any{
						"terminal-auth": map[string]any{
							"command": "claude",
							"args":    []string{"/login"},
							"label":   "Claude Code Login",
						},
					}
				}
			}
		}
	}

	title := "Claude Code"
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentCapabilities: acp.AgentCapabilities{
			PromptCapabilities: acp.PromptCapabilities{
				Image:           true,
				EmbeddedContext: true,
			},
			McpCapabilities: acp.McpCapabilities{
				Http: true,
				Sse:  true,
			},
			// LoadSession: false - not implemented yet
			// SessionCapabilities (fork, resume, list) - not implemented yet
		},
		AgentInfo: &acp.Implementation{
			Name:    "claude-code-acp",
			Title:   &title,
			Version: "0.1.0",
		},
		AuthMethods: []acp.AuthMethod{authMethod},
	}, nil
}

// Authenticate handles authentication requests.
func (a *ClaudeAcpAgent) Authenticate(_ context.Context, _ acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

// NewSession creates a new Claude Code session.
func (a *ClaudeAcpAgent) NewSession(ctx context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	if backupExistsWithoutPrimary() {
		return acp.NewSessionResponse{}, acp.NewAuthRequired(nil)
	}
	sessionID := generateID()

	settingsMgr := NewSettingsManager(params.Cwd, a.logger)
	if err := settingsMgr.Initialize(); err != nil {
		a.logger.Error("Failed to initialize settings", "error", err)
	}

	settings := settingsMgr.GetSettings()
	permissionMode := "default"
	if settings.Permissions != nil && settings.Permissions.DefaultMode != "" {
		permissionMode = settings.Permissions.DefaultMode
	}
	if permissionMode == "bypassPermissions" && !a.allowBypass {
		permissionMode = "default"
	}

	var maxThinkingTokens int
	if v := os.Getenv("MAX_THINKING_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			maxThinkingTokens = n
		}
	}

	executable := os.Getenv("CLAUDE_CODE_EXECUTABLE")

	// Extract system prompt from _meta if provided
	var systemPrompt string
	if params.Meta != nil {
		if meta, ok := params.Meta.(map[string]any); ok {
			if sp, ok := meta["systemPrompt"]; ok {
				if s, ok := sp.(string); ok {
					systemPrompt = s
				}
			}
		}
	}

	proc, err := NewClaudeCodeProcess(ClaudeCodeOptions{
		Cwd:               params.Cwd,
		SessionID:         sessionID,
		PermissionMode:    permissionMode,
		MaxTurns:          200,
		MaxThinkingTokens: maxThinkingTokens,
		Executable:        executable,
		SystemPrompt:      systemPrompt,
		McpServers:        mapMcpServers(params.McpServers),
	})
	if err != nil {
		return acp.NewSessionResponse{}, fmt.Errorf("failed to start Claude Code: %w", err)
	}

	session := &Session{
		process:         proc,
		permissionMode:  permissionMode,
		settingsManager: settingsMgr,
	}

	a.mu.Lock()
	a.sessions[sessionID] = session
	a.mu.Unlock()

	return acp.NewSessionResponse{
		SessionId: acp.SessionId(sessionID),
		Modes: &acp.SessionModeState{
			CurrentModeId:  acp.SessionModeId(permissionMode),
			AvailableModes: filterModes(a.allowBypass),
		},
	}, nil
}

// Prompt handles a user prompt by forwarding it to the Claude Code subprocess.
func (a *ClaudeAcpAgent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	sessionID := string(params.SessionId)

	a.mu.RLock()
	session, ok := a.sessions[sessionID]
	a.mu.RUnlock()
	if !ok {
		return acp.PromptResponse{}, fmt.Errorf("session not found: %s", sessionID)
	}

	session.ResetCancelled()

	msg := promptToClaude(params)
	if err := session.process.SendMessage(msg); err != nil {
		return acp.PromptResponse{}, fmt.Errorf("failed to send message: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		default:
		}

		if session.IsCancelled() {
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		}

		resp, err := session.process.ReadMessage()
		if err != nil {
			if err == io.EOF {
				if session.IsCancelled() {
					return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
				}
				return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
			}
			return acp.PromptResponse{}, fmt.Errorf("read error: %w", err)
		}

		switch resp.Type {
		case "system":
			// Skip system messages
			a.logger.Debug("Received system message", "subtype", resp.Subtype)
			continue

		case "result":
			a.logger.Debug("Received result", "subtype", resp.Subtype)
			if session.IsCancelled() {
				return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
			}
			return a.handleResult(resp)

		case "stream_event":
			if session.IsCancelled() {
				continue
			}
			// Use the raw line preserved in SDKResponse for accurate field access
			var raw map[string]any
			if resp.RawLine != nil {
				_ = json.Unmarshal(resp.RawLine, &raw)
			} else {
				line, _ := json.Marshal(resp)
				_ = json.Unmarshal(line, &raw)
			}
			parentID := getParentToolUseID(raw)
			notifications := streamEventToAcpNotifications(raw, sessionID, a.toolUseCache, parentID)
			a.logger.Debug("stream_event", "event_raw_keys", mapKeys(raw), "notifications", len(notifications))
			for _, n := range notifications {
				_ = a.conn.SessionUpdate(ctx, n)
			}
			if len(notifications) > 0 {
				session.MarkStreamEventsReceived()
			}

		case "assistant", "user":
			if session.IsCancelled() {
				continue
			}
			a.logger.Debug("Received message", "type", resp.Type)
			a.handleMessage(ctx, resp, sessionID, session)

		case "tool_progress", "tool_use_summary", "auth_status":
			continue

		default:
			a.logger.Warn("Unknown message type", "type", resp.Type)
		}
	}
}

func (a *ClaudeAcpAgent) handleResult(resp *SDKResponse) (acp.PromptResponse, error) {
	switch resp.Subtype {
	case "success":
		if strings.Contains(resp.Result, "Please run /login") {
			return acp.PromptResponse{}, acp.NewAuthRequired(nil)
		}
		if resp.IsError {
			return acp.PromptResponse{}, acp.NewInternalError(map[string]any{"error": resp.Result})
		}
		return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	case "error_max_turns", "error_max_budget_usd", "error_max_structured_output_retries":
		if resp.IsError {
			errMsg := strings.Join(resp.Errors, ", ")
			if errMsg == "" {
				errMsg = resp.Subtype
			}
			return acp.PromptResponse{}, acp.NewInternalError(map[string]any{"error": errMsg})
		}
		return acp.PromptResponse{StopReason: acp.StopReasonMaxTurnRequests}, nil
	case "error_during_execution":
		if resp.IsError {
			errMsg := strings.Join(resp.Errors, ", ")
			if errMsg == "" {
				errMsg = resp.Subtype
			}
			return acp.PromptResponse{}, acp.NewInternalError(map[string]any{"error": errMsg})
		}
		return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	default:
		return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	}
}

func (a *ClaudeAcpAgent) handleMessage(ctx context.Context, resp *SDKResponse, sessionID string, session *Session) {
	var msgData map[string]any
	if resp.Message != nil {
		json.Unmarshal(resp.Message, &msgData)
	}
	if msgData == nil {
		return
	}

	role, _ := msgData["role"].(string)
	content := msgData["content"]
	textContent, _ := content.(string)
	if textContent != "" {
		if strings.Contains(textContent, "<local-command-stdout>") {
			if strings.Contains(textContent, "Context Usage") {
				cleaned := strings.ReplaceAll(textContent, "<local-command-stdout>", "")
				cleaned = strings.ReplaceAll(cleaned, "</local-command-stdout>", "")
				for _, n := range toAcpNotifications(cleaned, "assistant", sessionID, a.toolUseCache, getParentToolUseIDFromResp(resp)) {
					_ = a.conn.SessionUpdate(ctx, n)
				}
			}
			return
		}
		if strings.Contains(textContent, "<local-command-stderr>") {
			a.logger.Error(textContent)
			return
		}
	}

	// Skip user messages that are plain text
	if resp.Type == "user" {
		if _, ok := content.(string); ok {
			return
		}
		if arr, ok := content.([]any); ok && len(arr) == 1 {
			if m, ok := arr[0].(map[string]any); ok {
				if m["type"] == "text" {
					return
				}
			}
		}
	}

	if resp.Type == "assistant" && isSyntheticLoginPrompt(content) {
		return
	}

	// Only filter text/thinking from assistant messages if stream_events already delivered them.
	// If no stream_events were received, keep text so the client gets the response.
	if resp.Type == "assistant" && textContent == "" && session.HasStreamEventsReceived() {
		if blocks, ok := content.([]any); ok {
			filtered := make([]any, 0, len(blocks))
			for _, block := range blocks {
				item, ok := block.(map[string]any)
				if !ok {
					filtered = append(filtered, block)
					continue
				}
				if kind, ok := item["type"].(string); ok && (kind == "text" || kind == "thinking") {
					continue
				}
				filtered = append(filtered, block)
			}
			content = filtered
		}
	}

	// For assistant messages with stream events, text/thinking would be duplicated.
	// But when we only receive full messages (no stream_event), we must keep them.
	// Since our CLI setup produces full messages, pass all content through.

	// Get parent_tool_use_id from the raw response
	parentID := getParentToolUseIDFromResp(resp)

	for _, n := range toAcpNotifications(content, role, sessionID, a.toolUseCache, parentID) {
		_ = a.conn.SessionUpdate(ctx, n)
	}
}

// Cancel cancels an ongoing session operation.
func (a *ClaudeAcpAgent) Cancel(_ context.Context, params acp.CancelNotification) error {
	sessionID := string(params.SessionId)
	a.mu.RLock()
	session, ok := a.sessions[sessionID]
	a.mu.RUnlock()
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	session.Cancel()
	_ = session.process.Close()
	return nil
}

// SetSessionMode changes the permission mode for a session.
func (a *ClaudeAcpAgent) SetSessionMode(_ context.Context, params acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	sessionID := string(params.SessionId)
	modeID := string(params.ModeId)

	a.mu.RLock()
	session, ok := a.sessions[sessionID]
	a.mu.RUnlock()
	if !ok {
		return acp.SetSessionModeResponse{}, fmt.Errorf("session not found: %s", sessionID)
	}

	validMode := false
	for _, m := range filterModes(a.allowBypass) {
		if string(m.Id) == modeID {
			validMode = true
			break
		}
	}
	if !validMode {
		return acp.SetSessionModeResponse{}, fmt.Errorf("invalid mode: %s", modeID)
	}

	session.SetPermissionMode(modeID)
	return acp.SetSessionModeResponse{}, nil
}

// promptToClaude converts an ACP PromptRequest to a Claude SDK user message.
func promptToClaude(req acp.PromptRequest) SDKUserMessage {
	var content []any
	var contextBlocks []any

	for _, block := range req.Prompt {
		if block.Text != nil {
			text := normalizeMcpSlashCommand(block.Text.Text)
			content = append(content, map[string]any{
				"type": "text",
				"text": text,
			})
		} else if block.ResourceLink != nil {
			uri := block.ResourceLink.Uri
			content = append(content, map[string]any{
				"type": "text",
				"text": formatUriAsLink(uri),
			})
		} else if block.Resource != nil {
			res := block.Resource.Resource
			if res.TextResourceContents != nil {
				uri := res.TextResourceContents.Uri
				text := res.TextResourceContents.Text
				content = append(content, map[string]any{
					"type": "text",
					"text": formatUriAsLink(uri),
				})
				contextBlocks = append(contextBlocks, map[string]any{
					"type": "text",
					"text": fmt.Sprintf("\n<context ref=%q>\n%s\n</context>", uri, text),
				})
			}
		} else if block.Image != nil {
			if block.Image.Data != "" {
				content = append(content, map[string]any{
					"type": "image",
					"source": map[string]any{
						"type":       "base64",
						"data":       block.Image.Data,
						"media_type": block.Image.MimeType,
					},
				})
			} else if block.Image.Uri != nil && strings.HasPrefix(*block.Image.Uri, "http") {
				content = append(content, map[string]any{
					"type": "image",
					"source": map[string]any{
						"type": "url",
						"url":  *block.Image.Uri,
					},
				})
			}
		}
	}

	content = append(content, contextBlocks...)

	return SDKUserMessage{
		Type: "user",
		Message: SDKMessage{
			Role:    "user",
			Content: content,
		},
		SessionID: string(req.SessionId),
	}
}

func getParentToolUseID(raw map[string]any) *string {
	if v, ok := raw["parent_tool_use_id"]; ok {
		if s, ok := v.(string); ok {
			return &s
		}
	}
	return nil
}

func getParentToolUseIDFromResp(resp *SDKResponse) *string {
	if resp.RawLine == nil {
		return nil
	}
	var raw map[string]any
	_ = json.Unmarshal(resp.RawLine, &raw)
	return getParentToolUseID(raw)
}

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	// Format as UUID v4: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func backupExistsWithoutPrimary() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	backup := filepath.Join(home, ".claude.json.backup")
	primary := filepath.Join(home, ".claude.json")
	if _, err := os.Stat(backup); err == nil {
		if _, err := os.Stat(primary); os.IsNotExist(err) {
			return true
		}
	}
	return false
}

func filterModes(allowBypass bool) []acp.SessionMode {
	if allowBypass {
		return validModes
	}
	modes := make([]acp.SessionMode, 0, len(validModes)-1)
	for _, mode := range validModes {
		if mode.Id == "bypassPermissions" {
			continue
		}
		modes = append(modes, mode)
	}
	return modes
}

func mapMcpServers(servers []acp.McpServer) map[string]McpServerConfig {
	if len(servers) == 0 {
		return nil
	}
	configs := make(map[string]McpServerConfig)
	for _, server := range servers {
		switch {
		case server.Http != nil:
			cfg := McpServerConfig{Type: "http", URL: server.Http.Url}
			if len(server.Http.Headers) > 0 {
				cfg.Headers = headersToMap(server.Http.Headers)
			}
			configs[server.Http.Name] = cfg
		case server.Sse != nil:
			cfg := McpServerConfig{Type: "sse", URL: server.Sse.Url}
			if len(server.Sse.Headers) > 0 {
				cfg.Headers = headersToMap(server.Sse.Headers)
			}
			configs[server.Sse.Name] = cfg
		case server.Stdio != nil:
			cfg := McpServerConfig{Type: "stdio", Command: server.Stdio.Command, Args: server.Stdio.Args}
			if len(server.Stdio.Env) > 0 {
				cfg.Env = envToMap(server.Stdio.Env)
			}
			configs[server.Stdio.Name] = cfg
		}
	}
	if len(configs) == 0 {
		return nil
	}
	return configs
}

func headersToMap(headers []acp.HttpHeader) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for _, header := range headers {
		out[header.Name] = header.Value
	}
	return out
}

func envToMap(env []acp.EnvVariable) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for _, entry := range env {
		out[entry.Name] = entry.Value
	}
	return out
}

func normalizeMcpSlashCommand(text string) string {
	match := mcpSlashCommandRe.FindStringSubmatch(text)
	if match == nil {
		return text
	}
	args := match[3]
	return fmt.Sprintf("/%s:%s (MCP)%s", match[1], match[2], args)
}

func formatUriAsLink(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		path := uri[7:] // Remove "file://"
		parts := strings.Split(path, "/")
		name := parts[len(parts)-1]
		if name == "" {
			name = path
		}
		return fmt.Sprintf("[@%s](%s)", name, uri)
	} else if strings.HasPrefix(uri, "zed://") {
		parts := strings.Split(uri, "/")
		name := parts[len(parts)-1]
		if name == "" {
			name = uri
		}
		return fmt.Sprintf("[@%s](%s)", name, uri)
	}
	return uri
}

func pathBase(uri string) string {
	if uri == "" {
		return ""
	}
	clean := strings.TrimSuffix(uri, "/")
	base := filepath.Base(clean)
	if base == "." || base == "/" {
		return uri
	}
	return base
}

func isSyntheticLoginPrompt(content any) bool {
	items, ok := content.([]any)
	if !ok || len(items) != 1 {
		return false
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		return false
	}
	if item["type"] != "text" {
		return false
	}
	text, ok := item["text"].(string)
	if !ok {
		return false
	}
	return strings.Contains(text, "Please run /login")
}

var mcpSlashCommandRe = regexp.MustCompile(`^/mcp:([^:\s]+):(\S+)(\s+.*)?$`)

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
