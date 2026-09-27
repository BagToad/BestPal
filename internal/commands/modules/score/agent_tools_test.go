package score

import (
	"encoding/json"
	"fmt"
	"testing"

	"gamerpal/internal/agentctx"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func agentTool(t *testing.T, m *Module, name string) copilot.Tool {
	t.Helper()
	for _, tl := range m.AgentTools() {
		if tl.Name == name {
			return tl
		}
	}
	t.Fatalf("tool %s not found", name)
	return copilot.Tool{}
}

func callThingsTool(t *testing.T, m *Module, name, sessionID string, args map[string]any) thingsResult {
	t.Helper()
	res, err := agentTool(t, m, name).Handler(copilot.ToolInvocation{SessionID: sessionID, ToolName: name, Arguments: args})
	require.NoError(t, err)
	var out thingsResult
	require.NoError(t, json.Unmarshal([]byte(res.TextResultForLLM), &out), res.TextResultForLLM)
	return out
}

func registerCaller(t *testing.T, sessionID string, c agentctx.Caller) {
	t.Helper()
	agentctx.Register(sessionID, c)
	t.Cleanup(func() { agentctx.Unregister(sessionID) })
}

func TestAgentTools_ExposesExpectedTools(t *testing.T) {
	m, _ := newTestModule(t, nil)
	var names []string
	for _, tl := range m.AgentTools() {
		names = append(names, tl.Name)
		assert.True(t, tl.SkipPermission, "tool %s should skip permission", tl.Name)
	}
	assert.ElementsMatch(t, []string{"get_user_things", "get_self_things", "list_things", "get_things_leaderboard"}, names)

	assert.Nil(t, (&Module{}).AgentTools(), "no tools without a database")
}

func TestAgentTools_UserThings(t *testing.T) {
	m, _ := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("Horses"), countOpt(24)))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("zebras")))
	registerCaller(t, "sess-user", agentctx.Caller{UserID: "asker", GuildID: "guild1"})

	res := callThingsTool(t, m, "get_user_things", "sess-user", map[string]any{"user_id": "<@user1>", "thing": "  horses "})
	assert.Equal(t, "found", res.Status)
	assert.Equal(t, "user1", res.UserID)
	assert.Equal(t, []agentThing{{Name: "Horses", Count: 24}}, res.Things)

	res = callThingsTool(t, m, "get_user_things", "sess-user", map[string]any{"user_id": "user1"})
	assert.Equal(t, "found", res.Status)
	assert.Equal(t, []agentThing{{"Horses", 24}, {"zebras", 1}}, res.Things)

	res = callThingsTool(t, m, "get_user_things", "sess-user", map[string]any{"user_id": "user1", "thing": "cows"})
	assert.Equal(t, "none", res.Status)
	assert.Equal(t, []agentThing{{"cows", 0}}, res.Things)

	res = callThingsTool(t, m, "get_user_things", "sess-user", map[string]any{"user_id": "nobody"})
	assert.Equal(t, "none", res.Status)
	assert.Empty(t, res.Things)

	res = callThingsTool(t, m, "get_user_things", "sess-user", map[string]any{"user_id": " "})
	assert.Equal(t, "none", res.Status)
}

func TestAgentTools_SelfThingsUsesHostCaller(t *testing.T) {
	m, _ := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(3)))
	registerCaller(t, "sess-self", agentctx.Caller{UserID: "user1", GuildID: "guild1"})

	res := callThingsTool(t, m, "get_self_things", "sess-self", map[string]any{"thing": "HORSES"})
	assert.Equal(t, "found", res.Status)
	assert.Equal(t, "user1", res.UserID)
	assert.Equal(t, []agentThing{{"horses", 3}}, res.Things)

	res = callThingsTool(t, m, "get_self_things", "unknown-session", map[string]any{})
	assert.Equal(t, "unavailable", res.Status)
}

func TestAgentTools_ScopedToCallerGuild(t *testing.T) {
	m, _ := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses")))
	registerCaller(t, "sess-other", agentctx.Caller{UserID: "user1", GuildID: "guild2"})
	registerCaller(t, "sess-dm", agentctx.Caller{UserID: "user1"})

	res := callThingsTool(t, m, "get_self_things", "sess-other", map[string]any{"thing": "horses"})
	assert.Equal(t, "none", res.Status, "another server's things aren't visible")

	res = callThingsTool(t, m, "get_user_things", "sess-dm", map[string]any{"user_id": "user1"})
	assert.Equal(t, "unavailable", res.Status)
}

func TestAgentTools_ListTruncated(t *testing.T) {
	m, _ := newTestModule(t, nil)
	for n := 0; n < maxAgentThings+5; n++ {
		m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("thing"+string(rune('A'+n%26))+string(rune('a'+n/26)))))
	}

	res := m.lookupThings("guild1", "user1", "")
	assert.Equal(t, "found", res.Status)
	assert.Len(t, res.Things, maxAgentThings)
	assert.True(t, res.Truncated)
}

func TestAgentTools_LoadFailure(t *testing.T) {
	m, _ := newTestModule(t, failingStore{})

	res := m.lookupThings("guild1", "user1", "horses")
	assert.Equal(t, "unavailable", res.Status)

	registerCaller(t, "sess-fail", agentctx.Caller{UserID: "user1", GuildID: "guild1"})
	names := callListThingsTool(t, m, "sess-fail", map[string]any{})
	assert.Equal(t, "unavailable", names.Status)
}

func callListThingsTool(t *testing.T, m *Module, sessionID string, args map[string]any) thingNamesResult {
	t.Helper()
	res, err := agentTool(t, m, "list_things").Handler(copilot.ToolInvocation{SessionID: sessionID, ToolName: "list_things", Arguments: args})
	require.NoError(t, err)
	var out thingNamesResult
	require.NoError(t, json.Unmarshal([]byte(res.TextResultForLLM), &out), res.TextResultForLLM)
	return out
}

func TestAgentTools_ListThings(t *testing.T) {
	m, _ := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("Horses")))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user2"), thingOpt("horses")))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user2"), thingOpt("cookies")))
	registerCaller(t, "sess-list", agentctx.Caller{UserID: "asker", GuildID: "guild1"})
	registerCaller(t, "sess-list-other", agentctx.Caller{UserID: "asker", GuildID: "guild2"})
	registerCaller(t, "sess-list-dm", agentctx.Caller{UserID: "asker"})

	res := callListThingsTool(t, m, "sess-list", map[string]any{})
	assert.Equal(t, "found", res.Status)
	assert.Equal(t, []string{"cookies", "Horses"}, res.Names, "each thing once, first-given spelling")

	res = callListThingsTool(t, m, "sess-list", map[string]any{"query": "HORSE"})
	assert.Equal(t, []string{"Horses"}, res.Names)

	res = callListThingsTool(t, m, "sess-list", map[string]any{"query": "zebra"})
	assert.Equal(t, "none", res.Status)

	res = callListThingsTool(t, m, "sess-list-other", map[string]any{})
	assert.Equal(t, "none", res.Status, "another server's things aren't visible")

	res = callListThingsTool(t, m, "sess-list-dm", map[string]any{})
	assert.Equal(t, "unavailable", res.Status)
}

func TestAgentTools_MissSuggestsSimilarNames(t *testing.T) {
	m, _ := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("horses"), countOpt(4)))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user2"), thingOpt("seahorse")))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user2"), thingOpt("cookies")))

	res := m.lookupThings("guild1", "user1", "horse")
	assert.Equal(t, "none", res.Status)
	assert.Equal(t, []string{"horses", "seahorse"}, res.Suggestions)

	res = m.lookupThings("guild1", "user2", "horses")
	assert.Equal(t, "none", res.Status)
	assert.Empty(t, res.Suggestions, "horses isn't contained in seahorse, nor vice versa")

	res = m.lookupThings("guild1", "user1", "horses")
	assert.Equal(t, "found", res.Status)
	assert.Empty(t, res.Suggestions)
}

func callLeaderboardTool(t *testing.T, m *Module, sessionID string, args map[string]any) leaderboardResult {
	t.Helper()
	res, err := agentTool(t, m, "get_things_leaderboard").Handler(copilot.ToolInvocation{SessionID: sessionID, ToolName: "get_things_leaderboard", Arguments: args})
	require.NoError(t, err)
	var out leaderboardResult
	require.NoError(t, json.Unmarshal([]byte(res.TextResultForLLM), &out), res.TextResultForLLM)
	return out
}

func TestAgentTools_Leaderboard(t *testing.T) {
	m, c := newTestModule(t, nil)
	m.handleGive(nil, interaction("give", "mod1", userOpt("user1"), thingOpt("Horses"), countOpt(5)))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user2"), thingOpt("horses"), countOpt(9)))
	m.handleGive(nil, interaction("give", "mod1", userOpt("user3"), thingOpt("horses"), countOpt(5)))
	m.handleGive(nil, interaction("give", "mod1", userOpt("gone"), thingOpt("horses"), countOpt(100)))
	c.left = map[string]bool{"gone": true}
	registerCaller(t, "sess-lb", agentctx.Caller{UserID: "user1", GuildID: "guild1"})
	registerCaller(t, "sess-lb-dm", agentctx.Caller{UserID: "user1"})

	res := callLeaderboardTool(t, m, "sess-lb", map[string]any{"thing": "HORSES"})
	assert.Equal(t, "found", res.Status)
	assert.Equal(t, "Horses", res.Thing)
	assert.Equal(t, []agentLeaderboardEntry{{1, "user2", 9}, {2, "user1", 5}, {2, "user3", 5}}, res.Entries, "tied counts share a rank, departed members are skipped")
	assert.Nil(t, res.Caller, "caller is already in the top list")

	res = callLeaderboardTool(t, m, "sess-lb", map[string]any{"thing": "horse"})
	assert.Equal(t, "none", res.Status)
	assert.Equal(t, []string{"Horses"}, res.Suggestions)

	res = callLeaderboardTool(t, m, "sess-lb", map[string]any{"thing": " "})
	assert.Equal(t, "none", res.Status)

	res = callLeaderboardTool(t, m, "sess-lb-dm", map[string]any{"thing": "horses"})
	assert.Equal(t, "unavailable", res.Status)
}

func TestAgentTools_LeaderboardIncludesCallerOutsideTop(t *testing.T) {
	m, _ := newTestModule(t, nil)
	for n := 0; n < leaderboardSize; n++ {
		m.handleGive(nil, interaction("give", "mod1", userOpt(fmt.Sprintf("top%02d", n)), thingOpt("horses"), countOpt(10)))
	}
	m.handleGive(nil, interaction("give", "mod1", userOpt("asker"), thingOpt("horses"), countOpt(2)))
	registerCaller(t, "sess-lb-rank", agentctx.Caller{UserID: "asker", GuildID: "guild1"})
	registerCaller(t, "sess-lb-none", agentctx.Caller{UserID: "nobody", GuildID: "guild1"})

	res := callLeaderboardTool(t, m, "sess-lb-rank", map[string]any{"thing": "horses"})
	assert.Len(t, res.Entries, leaderboardSize)
	assert.Equal(t, &agentLeaderboardEntry{Rank: leaderboardSize + 1, UserID: "asker", Count: 2}, res.Caller)

	res = callLeaderboardTool(t, m, "sess-lb-none", map[string]any{"thing": "horses"})
	assert.Nil(t, res.Caller, "callers holding none get no spot")
}

func TestAgentTools_LeaderboardLoadFailure(t *testing.T) {
	m, _ := newTestModule(t, failingStore{})
	res := m.lookupLeaderboard("guild1", "user1", "horses")
	assert.Equal(t, "unavailable", res.Status)
}
