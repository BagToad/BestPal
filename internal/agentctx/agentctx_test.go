package agentctx

import (
	"sync"
	"testing"
)

func TestRegistry_Table(t *testing.T) {
	type op struct {
		kind      string // "register" | "unregister" | "lookup"
		sessionID string
		caller    Caller
	}

	tests := []struct {
		name     string
		ops      []op
		lookupID string
		wantOK   bool
		wantUser string
	}{
		{
			name: "register then lookup returns caller",
			ops: []op{
				{kind: "register", sessionID: "s1", caller: Caller{UserID: "u1", GuildID: "g1"}},
			},
			lookupID: "s1",
			wantOK:   true,
			wantUser: "u1",
		},
		{
			name:     "lookup unknown session returns false",
			ops:      nil,
			lookupID: "missing",
			wantOK:   false,
		},
		{
			name: "unregister removes entry",
			ops: []op{
				{kind: "register", sessionID: "s2", caller: Caller{UserID: "u2"}},
				{kind: "unregister", sessionID: "s2"},
			},
			lookupID: "s2",
			wantOK:   false,
		},
		{
			name: "second register overwrites first",
			ops: []op{
				{kind: "register", sessionID: "s3", caller: Caller{UserID: "first"}},
				{kind: "register", sessionID: "s3", caller: Caller{UserID: "second"}},
			},
			lookupID: "s3",
			wantOK:   true,
			wantUser: "second",
		},
		{
			name: "register with empty session id is no-op",
			ops: []op{
				{kind: "register", sessionID: "", caller: Caller{UserID: "ghost"}},
			},
			lookupID: "",
			wantOK:   false,
		},
		{
			name: "lookup with empty session id always returns false",
			ops: []op{
				{kind: "register", sessionID: "s4", caller: Caller{UserID: "u4"}},
			},
			lookupID: "",
			wantOK:   false,
		},
		{
			name: "unregister empty session id is no-op",
			ops: []op{
				{kind: "register", sessionID: "s5", caller: Caller{UserID: "u5"}},
				{kind: "unregister", sessionID: ""},
			},
			lookupID: "s5",
			wantOK:   true,
			wantUser: "u5",
		},
		{
			name: "unregister unknown session is no-op",
			ops: []op{
				{kind: "unregister", sessionID: "never-registered"},
			},
			lookupID: "never-registered",
			wantOK:   false,
		},
		{
			name: "register, unregister, register again returns new value",
			ops: []op{
				{kind: "register", sessionID: "s6", caller: Caller{UserID: "old"}},
				{kind: "unregister", sessionID: "s6"},
				{kind: "register", sessionID: "s6", caller: Caller{UserID: "new"}},
			},
			lookupID: "s6",
			wantOK:   true,
			wantUser: "new",
		},
		{
			name: "entries are isolated by session id",
			ops: []op{
				{kind: "register", sessionID: "a", caller: Caller{UserID: "alice"}},
				{kind: "register", sessionID: "b", caller: Caller{UserID: "bob"}},
				{kind: "unregister", sessionID: "a"},
			},
			lookupID: "b",
			wantOK:   true,
			wantUser: "bob",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(reset)
			for _, o := range tc.ops {
				switch o.kind {
				case "register":
					Register(o.sessionID, o.caller)
				case "unregister":
					Unregister(o.sessionID)
				default:
					t.Fatalf("unknown op kind %q", o.kind)
				}
			}
			got, ok := CallerForSession(tc.lookupID)
			if ok != tc.wantOK {
				t.Fatalf("CallerForSession(%q) ok = %v, want %v", tc.lookupID, ok, tc.wantOK)
			}
			if ok && got.UserID != tc.wantUser {
				t.Fatalf("CallerForSession(%q).UserID = %q, want %q", tc.lookupID, got.UserID, tc.wantUser)
			}
		})
	}
}

// TestConcurrentRegisterAndRead is intentionally not part of the table: it
// exercises the RWMutex under -race rather than asserting an outcome.
func TestConcurrentRegisterAndRead(t *testing.T) {
	defer reset()

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			Register("s", Caller{UserID: "u"})
		}()
		go func() {
			defer wg.Done()
			_, _ = CallerForSession("s")
		}()
	}
	wg.Wait()
}

func reset() {
	registryMu.Lock()
	registry = map[string]Caller{}
	registryMu.Unlock()
}
