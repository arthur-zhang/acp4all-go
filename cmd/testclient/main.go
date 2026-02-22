package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

type testClient struct{}

var _ acp.Client = (*testClient)(nil)

func (c *testClient) RequestPermission(_ context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}
	fmt.Fprintf(os.Stderr, "🔐 Permission requested: %s\n", title)
	// Auto-allow for testing
	if len(params.Options) > 0 {
		return acp.RequestPermissionResponse{
			Outcome: acp.RequestPermissionOutcome{
				Selected: &acp.RequestPermissionOutcomeSelected{
					OptionId: params.Options[0].OptionId,
				},
			},
		}, nil
	}
	return acp.RequestPermissionResponse{}, nil
}

func (c *testClient) SessionUpdate(_ context.Context, params acp.SessionNotification) error {
	u := params.Update
	switch {
	case u.AgentMessageChunk != nil:
		cb := u.AgentMessageChunk.Content
		if cb.Text != nil {
			fmt.Print(cb.Text.Text)
		}
	case u.AgentThoughtChunk != nil:
		cb := u.AgentThoughtChunk.Content
		if cb.Text != nil {
			fmt.Fprintf(os.Stderr, "💭 %s", cb.Text.Text)
		}
	case u.ToolCall != nil:
		fmt.Fprintf(os.Stderr, "\n🔧 %s [%s]\n", u.ToolCall.Title, u.ToolCall.Status)
	case u.ToolCallUpdate != nil:
		status := ""
		if u.ToolCallUpdate.Status != nil {
			status = string(*u.ToolCallUpdate.Status)
		}
		fmt.Fprintf(os.Stderr, "🔧 Tool %s → %s\n", u.ToolCallUpdate.ToolCallId, status)
	case u.Plan != nil:
		fmt.Fprintf(os.Stderr, "📋 Plan updated (%d entries)\n", len(u.Plan.Entries))
	}
	return nil
}

func (c *testClient) ReadTextFile(_ context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	data, err := os.ReadFile(params.Path)
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	return acp.ReadTextFileResponse{Content: string(data)}, nil
}

func (c *testClient) WriteTextFile(_ context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	if err := os.WriteFile(params.Path, []byte(params.Content), 0o644); err != nil {
		return acp.WriteTextFileResponse{}, err
	}
	return acp.WriteTextFileResponse{}, nil
}

func (c *testClient) CreateTerminal(_ context.Context, _ acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, fmt.Errorf("terminal not supported in test client")
}

func (c *testClient) KillTerminalCommand(_ context.Context, _ acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
	return acp.KillTerminalCommandResponse{}, nil
}

func (c *testClient) TerminalOutput(_ context.Context, _ acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, nil
}

func (c *testClient) ReleaseTerminal(_ context.Context, _ acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, nil
}

func (c *testClient) WaitForTerminalExit(_ context.Context, _ acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Build path to our agent binary
	agentBin := "./claude-code-acp-go"

	cmd := exec.CommandContext(ctx, agentBin)
	cmd.Stderr = os.Stderr
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to start agent: %v\n", err)
		os.Exit(1)
	}
	defer cmd.Process.Kill()

	client := &testClient{}
	conn := acp.NewClientSideConnection(client, stdin, stdout)

	// Step 1: Initialize
	fmt.Fprintf(os.Stderr, "→ Sending initialize...\n")
	initResp, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs:       acp.FileSystemCapability{ReadTextFile: true, WriteTextFile: true},
			Terminal: true,
		},
	})
	if err != nil {
		b, _ := json.MarshalIndent(err, "", "  ")
		fmt.Fprintf(os.Stderr, "❌ Initialize error: %s\n", string(b))
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "✅ Connected (protocol v%d)\n", initResp.ProtocolVersion)
	if initResp.AgentInfo != nil {
		fmt.Fprintf(os.Stderr, "   Agent: %s v%s\n", initResp.AgentInfo.Name, initResp.AgentInfo.Version)
	}

	// Step 2: New Session
	cwd, _ := os.Getwd()
	fmt.Fprintf(os.Stderr, "→ Creating session (cwd=%s)...\n", cwd)
	sessResp, err := conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		b, _ := json.MarshalIndent(err, "", "  ")
		fmt.Fprintf(os.Stderr, "❌ NewSession error: %s\n", string(b))
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "✅ Session created: %s\n", sessResp.SessionId)
	if sessResp.Modes != nil {
		fmt.Fprintf(os.Stderr, "   Mode: %s\n", sessResp.Modes.CurrentModeId)
	}

	// Step 3: Send a simple prompt
	prompt := "What is 2+2? Reply with just the number."
	fmt.Fprintf(os.Stderr, "→ Sending prompt: %q\n\n", prompt)

	promptResp, err := conn.Prompt(ctx, acp.PromptRequest{
		SessionId: sessResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock(prompt)},
	})
	if err != nil {
		b, _ := json.MarshalIndent(err, "", "  ")
		fmt.Fprintf(os.Stderr, "\n❌ Prompt error: %s\n", string(b))
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\n✅ Prompt completed (stopReason=%s)\n", promptResp.StopReason)
}
