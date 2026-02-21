package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// mockClient implements acp.Client for testing
type mockClient struct {
	mu             sync.Mutex
	files          map[string]string
	sessionUpdates []acp.SessionNotification
	permissionAuto bool // auto-allow permissions
	terminals      map[string]*mockTerminal
	nextTerminalID int
}

type mockTerminal struct {
	command   string
	output    string
	exitCode  *int
	completed bool
}

func newMockClient() *mockClient {
	return &mockClient{
		files:          make(map[string]string),
		sessionUpdates: make([]acp.SessionNotification, 0),
		permissionAuto: true,
		terminals:      make(map[string]*mockTerminal),
	}
}

func (c *mockClient) ReadTextFile(_ context.Context, req acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	content, ok := c.files[req.Path]
	if !ok {
		return acp.ReadTextFileResponse{}, &acp.RequestError{Code: -32603, Message: "File not found: " + req.Path}
	}
	return acp.ReadTextFileResponse{Content: content}, nil
}

func (c *mockClient) WriteTextFile(_ context.Context, req acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.files[req.Path] = req.Content
	return acp.WriteTextFileResponse{}, nil
}

func (c *mockClient) RequestPermission(_ context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.permissionAuto && len(req.Options) > 0 {
		return acp.RequestPermissionResponse{
			Outcome: acp.RequestPermissionOutcome{
				Selected: &acp.RequestPermissionOutcomeSelected{OptionId: req.Options[0].OptionId},
			},
		}, nil
	}
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
	}, nil
}

func (c *mockClient) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessionUpdates = append(c.sessionUpdates, n)
	return nil
}

func (c *mockClient) CreateTerminal(_ context.Context, req acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextTerminalID++
	id := "term-" + string(rune('0'+c.nextTerminalID))
	exitCode := 0
	c.terminals[id] = &mockTerminal{
		command: req.Command, output: "mock output for: " + req.Command,
		exitCode: &exitCode, completed: true,
	}
	return acp.CreateTerminalResponse{TerminalId: id}, nil
}

func (c *mockClient) KillTerminalCommand(context.Context, acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
	return acp.KillTerminalCommandResponse{}, nil
}

func (c *mockClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, nil
}

func (c *mockClient) TerminalOutput(_ context.Context, req acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	term, ok := c.terminals[req.TerminalId]
	if !ok {
		return acp.TerminalOutputResponse{}, &acp.RequestError{Code: -32603, Message: "Terminal not found"}
	}
	return acp.TerminalOutputResponse{Output: term.output, Truncated: false}, nil
}

func (c *mockClient) WaitForTerminalExit(_ context.Context, req acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	term, ok := c.terminals[req.TerminalId]
	if !ok {
		return acp.WaitForTerminalExitResponse{}, &acp.RequestError{Code: -32603, Message: "Terminal not found"}
	}
	return acp.WaitForTerminalExitResponse{ExitCode: term.exitCode}, nil
}

func (c *mockClient) getSessionUpdates() []acp.SessionNotification {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]acp.SessionNotification, len(c.sessionUpdates))
	copy(result, c.sessionUpdates)
	return result
}

func (c *mockClient) setFile(path, content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.files[path] = content
}

// setupTestConnection creates a test connection between client and agent
func setupTestConnection(t *testing.T) (*acp.ClientSideConnection, *mockClient, func()) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c2aR, c2aW := io.Pipe()
	a2cR, a2cW := io.Pipe()
	client := newMockClient()
	clientConn := acp.NewClientSideConnection(client, c2aW, a2cR)
	clientConn.SetLogger(logger)
	agent := NewClaudeAcpAgent(logger)
	agentConn := acp.NewAgentSideConnection(agent, a2cW, c2aR)
	agentConn.SetLogger(logger)
	agent.SetAgentConnection(agentConn)
	cleanup := func() {
		c2aW.Close()
		a2cW.Close()
	}
	return clientConn, client, cleanup
}

// requireCLI checks if claude CLI is available and CLAUDECODE is unset
func requireCLI(t *testing.T) {
	t.Helper()
	// Unset CLAUDECODE to allow nested sessions in tests
	t.Setenv("CLAUDECODE", "")
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not found, skipping integration test")
	}
}

// --- Protocol-level tests (no CLI needed) ---

func TestIntegration_Initialize(t *testing.T) {
	conn, _, cleanup := setupTestConnection(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs:       acp.FileSystemCapability{ReadTextFile: true, WriteTextFile: true},
			Terminal: true,
		},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if resp.ProtocolVersion != acp.ProtocolVersionNumber {
		t.Errorf("Protocol version: got %d, want %d", resp.ProtocolVersion, acp.ProtocolVersionNumber)
	}
	if resp.AgentInfo == nil || resp.AgentInfo.Name != "claude-code-acp" {
		t.Errorf("AgentInfo.Name: got %v", resp.AgentInfo)
	}
	if len(resp.AuthMethods) == 0 {
		t.Error("Expected at least one auth method")
	}
}

func TestIntegration_AgentCapabilities(t *testing.T) {
	conn, _, cleanup := setupTestConnection(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{Fs: acp.FileSystemCapability{ReadTextFile: true, WriteTextFile: true}, Terminal: true},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if !resp.AgentCapabilities.PromptCapabilities.Image {
		t.Error("Should support image prompts")
	}
	if !resp.AgentCapabilities.PromptCapabilities.EmbeddedContext {
		t.Error("Should support embedded context")
	}
	if !resp.AgentCapabilities.McpCapabilities.Http {
		t.Error("Should support HTTP MCP")
	}
	if !resp.AgentCapabilities.McpCapabilities.Sse {
		t.Error("Should support SSE MCP")
	}
}

// --- Tests requiring CLI ---

func TestIntegration_NewSession(t *testing.T) {
	requireCLI(t)
	conn, _, cleanup := setupTestConnection(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{Fs: acp.FileSystemCapability{ReadTextFile: true, WriteTextFile: true}, Terminal: true},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	cwd, _ := os.Getwd()
	sessResp, err := conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if sessResp.SessionId == "" {
		t.Error("SessionId should not be empty")
	}
	sessionID := string(sessResp.SessionId)
	if len(sessionID) != 36 || strings.Count(sessionID, "-") != 4 {
		t.Errorf("SessionId should be UUID format, got %q", sessionID)
	}
	if sessResp.Modes == nil {
		t.Fatal("Modes should not be nil")
	}
	if sessResp.Modes.CurrentModeId == "" {
		t.Error("CurrentModeId should not be empty")
	}
	if len(sessResp.Modes.AvailableModes) == 0 {
		t.Error("AvailableModes should not be empty")
	}
}

func TestIntegration_SetSessionMode(t *testing.T) {
	requireCLI(t)
	conn, _, cleanup := setupTestConnection(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	cwd, _ := os.Getwd()
	sessResp, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	for _, mode := range []string{"default", "acceptEdits", "plan", "dontAsk"} {
		_, err := conn.SetSessionMode(ctx, acp.SetSessionModeRequest{
			SessionId: sessResp.SessionId,
			ModeId:    acp.SessionModeId(mode),
		})
		if err != nil {
			t.Errorf("SetSessionMode(%s) failed: %v", mode, err)
		}
	}

	_, err = conn.SetSessionMode(ctx, acp.SetSessionModeRequest{
		SessionId: sessResp.SessionId,
		ModeId:    "invalidMode",
	})
	if err == nil {
		t.Error("SetSessionMode with invalid mode should fail")
	}
}

func TestIntegration_Cancel(t *testing.T) {
	requireCLI(t)
	conn, _, cleanup := setupTestConnection(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	cwd, _ := os.Getwd()
	sessResp, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	err = conn.Cancel(ctx, acp.CancelNotification{SessionId: sessResp.SessionId})
	if err != nil {
		t.Errorf("Cancel failed: %v", err)
	}
}

func TestIntegration_MultipleSessionsIsolation(t *testing.T) {
	requireCLI(t)
	conn, _, cleanup := setupTestConnection(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	cwd, _ := os.Getwd()
	sess1, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("NewSession 1 failed: %v", err)
	}
	sess2, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("NewSession 2 failed: %v", err)
	}
	if sess1.SessionId == sess2.SessionId {
		t.Error("Sessions should have different IDs")
	}

	_, err = conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: sess1.SessionId, ModeId: "plan"})
	if err != nil {
		t.Errorf("SetSessionMode session 1: %v", err)
	}
	_, err = conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: sess2.SessionId, ModeId: "acceptEdits"})
	if err != nil {
		t.Errorf("SetSessionMode session 2: %v", err)
	}

	// Cancel one shouldn't affect the other
	_ = conn.Cancel(ctx, acp.CancelNotification{SessionId: sess1.SessionId})
	_, err = conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: sess2.SessionId, ModeId: "default"})
	if err != nil {
		t.Errorf("Session 2 should still work after cancelling session 1: %v", err)
	}
}

func TestIntegration_InvalidSession(t *testing.T) {
	requireCLI(t)
	conn, _, cleanup := setupTestConnection(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	_, err = conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: "non-existent", ModeId: "default"})
	if err == nil {
		t.Error("SetSessionMode on non-existent session should fail")
	}

	// Cancel is a notification in ACP protocol, errors may not propagate back
	// Just verify it doesn't panic
	_ = conn.Cancel(ctx, acp.CancelNotification{SessionId: "non-existent"})
}

func TestIntegration_AvailableModes(t *testing.T) {
	requireCLI(t)
	conn, _, cleanup := setupTestConnection(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	cwd, _ := os.Getwd()
	sessResp, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if sessResp.Modes == nil {
		t.Fatal("Modes should not be nil")
	}

	expectedModes := map[string]bool{"default": false, "acceptEdits": false, "plan": false, "dontAsk": false}
	for _, mode := range sessResp.Modes.AvailableModes {
		if _, ok := expectedModes[string(mode.Id)]; ok {
			expectedModes[string(mode.Id)] = true
		}
	}
	for mode, found := range expectedModes {
		if !found {
			t.Errorf("Expected mode %q not found", mode)
		}
	}
}
