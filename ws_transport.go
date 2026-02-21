package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"

	acp "github.com/coder/acp-go-sdk"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for local development
	},
}

// wsReadWriter bridges a WebSocket connection to io.Reader and io.Writer.
// It reads JSON messages from WebSocket and writes JSON messages to WebSocket.
// The ACP SDK expects newline-delimited JSON (ndjson) over the stream, so we
// ensure proper message framing between WebSocket messages and the ndjson stream.
type wsReadWriter struct {
	conn   *websocket.Conn
	mu     sync.Mutex // protects writes
	reader io.Reader  // current message reader
}

func newWSReadWriter(conn *websocket.Conn) *wsReadWriter {
	return &wsReadWriter{conn: conn}
}

// Read implements io.Reader by reading from WebSocket messages.
// Each WebSocket message is a complete JSON-RPC message. We append a newline
// after each message so the SDK's line-based scanner can delimit messages.
func (w *wsReadWriter) Read(p []byte) (int, error) {
	for {
		if w.reader != nil {
			n, err := w.reader.Read(p)
			if err == io.EOF {
				w.reader = nil
				// Append newline delimiter after the message content
				if n < len(p) {
					p[n] = '\n'
					return n + 1, nil
				}
				return n, nil
			}
			return n, err
		}
		_, reader, err := w.conn.NextReader()
		if err != nil {
			return 0, err
		}
		w.reader = reader
	}
}

// Write implements io.Writer by sending each write as a WebSocket text message.
// The ACP SDK writes JSON followed by a newline; we forward the bytes as-is.
func (w *wsReadWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.conn.WriteMessage(websocket.TextMessage, p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// RunWebSocketServer starts a WebSocket server that accepts ACP connections.
// Each incoming WebSocket connection gets its own AgentSideConnection and
// ClaudeAcpAgent instance, mirroring the TypeScript implementation pattern.
func RunWebSocketServer(host string, port int, logger *slog.Logger) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logger.Error("WebSocket upgrade failed", "error", err)
			return
		}
		defer conn.Close()

		logger.Info("New WebSocket connection from client")

		rw := newWSReadWriter(conn)
		agent := NewClaudeAcpAgent(logger)
		acpConn := acp.NewAgentSideConnection(agent, rw, rw)
		acpConn.SetLogger(logger)
		agent.SetAgentConnection(acpConn)

		// Block until the ACP connection is closed (peer disconnects).
		<-acpConn.Done()
		logger.Info("WebSocket connection closed")
	})

	addr := fmt.Sprintf("%s:%d", host, port)
	logger.Info("WebSocket server listening", "address", addr)
	return http.ListenAndServe(addr, mux)
}
