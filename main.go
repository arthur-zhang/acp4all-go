package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	acp "github.com/coder/acp-go-sdk"
)

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "Unhandled panic: %v\n", r)
			os.Exit(1)
		}
	}()

	// Load managed settings and apply environment variables
	if settings := loadManagedSettings(); settings != nil {
		applyEnvironmentSettings(settings)
	}

	transport := flag.String("transport", "stdio", "Transport mode: stdio or websocket")
	port := flag.Int("port", 8080, "Port for WebSocket server")
	host := flag.String("host", "127.0.0.1", "Host for WebSocket server")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	switch *transport {
	case "websocket":
		if err := RunWebSocketServer(*host, *port, logger); err != nil {
			logger.Error("WebSocket server error", "error", err)
			os.Exit(1)
		}
	default:
		// stdio mode: use stdin/stdout for ACP communication
		agent := NewClaudeAcpAgent(logger)
		conn := acp.NewAgentSideConnection(agent, os.Stdout, os.Stdin)
		conn.SetLogger(logger)
		agent.SetAgentConnection(conn)

		// Block until the connection is closed
		<-conn.Done()
	}
}
