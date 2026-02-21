package main

import (
	"sync"
)

// Session represents an active Claude Code session
type Session struct {
	process              *ClaudeCodeProcess
	cancelled            bool
	streamEventsReceived bool
	permissionMode       string // "default"|"acceptEdits"|"bypassPermissions"|"dontAsk"|"plan"
	settingsManager      *SettingsManager
	mu                   sync.Mutex
}

// Cancel marks the session as cancelled
func (s *Session) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelled = true
}

// IsCancelled returns whether the session has been cancelled
func (s *Session) IsCancelled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelled
}

// ResetCancelled resets the cancelled flag and stream events tracking
func (s *Session) ResetCancelled() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelled = false
	s.streamEventsReceived = false
}

// MarkStreamEventsReceived records that stream events were received for this prompt
func (s *Session) MarkStreamEventsReceived() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.streamEventsReceived = true
}

// HasStreamEventsReceived returns whether stream events were received
func (s *Session) HasStreamEventsReceived() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streamEventsReceived
}

// SetPermissionMode updates the session's permission mode
func (s *Session) SetPermissionMode(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.permissionMode = mode
}

// GetPermissionMode returns the current permission mode
func (s *Session) GetPermissionMode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.permissionMode
}

// BackgroundTerminal represents a terminal running in the background
type BackgroundTerminal struct {
	ID            string
	Status        string // "started"|"aborted"|"exited"|"killed"|"timedOut"
	LastOutput    string
	PendingOutput *TerminalOutput
}

// TerminalOutput holds terminal command output
type TerminalOutput struct {
	Output     string
	ExitCode   *int
	Signal     string
	Truncated  bool
}
