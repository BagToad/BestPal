// Package agentctx is a session-keyed registry of Caller identities used by
// agent tool handlers. The agent host registers a Caller against the Copilot
// SDK session ID before handing the session to the model; tools look it up
// via copilot.ToolInvocation.SessionID.
package agentctx

import "sync"

// Caller is the Discord user whose @mention spawned an agent session.
type Caller struct {
	UserID    string
	GuildID   string
	ChannelID string
	IsAdmin   bool
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Caller{}
)

// Register associates a Caller with a session ID. Overwrites any prior
// entry for the same ID. Empty sessionID is a no-op.
func Register(sessionID string, c Caller) {
	if sessionID == "" {
		return
	}
	registryMu.Lock()
	registry[sessionID] = c
	registryMu.Unlock()
}

// Unregister removes the Caller for a session ID. Safe to call multiple times.
func Unregister(sessionID string) {
	if sessionID == "" {
		return
	}
	registryMu.Lock()
	delete(registry, sessionID)
	registryMu.Unlock()
}

// CallerForSession returns the Caller registered for sessionID, or
// (zero, false) if none.
func CallerForSession(sessionID string) (Caller, bool) {
	if sessionID == "" {
		return Caller{}, false
	}
	registryMu.RLock()
	c, ok := registry[sessionID]
	registryMu.RUnlock()
	return c, ok
}
