package intro

import (
	"errors"
	"strings"
	"testing"

	"gamerpal/internal/agentctx"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/forumcache"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testIntroForumID = "forumA"
	testGuildID      = "guild1"
	testUserID       = "user1"
	testThreadID     = "700"
)

// newIntroModuleWithThread builds an intro module backed by a test forum cache.
// When seed is true a thread owned by testUserID is inserted. No Discord session
// is attached, so the content path resolves metadata but reports the body as
// unreadable, which is enough to exercise caller resolution.
func newIntroModuleWithThread(t *testing.T, seed bool) *Module {
	t.Helper()
	cfg, fc := forumcache.NewTestForumCache(map[string]any{"gamerpals_introductions_forum_channel_id": testIntroForumID})
	fc.RegisterForum(testIntroForumID)
	if seed {
		fc.OnThreadCreate(nil, &discordgo.ThreadCreate{Channel: &discordgo.Channel{ID: testThreadID, ParentID: testIntroForumID, GuildID: testGuildID, OwnerID: testUserID, Name: "intro"}})
	}
	return New(&types.Dependencies{Config: cfg, ForumCache: fc})
}

func testIntroMeta() *forumcache.ThreadMeta {
	return &forumcache.ThreadMeta{ID: testThreadID, GuildID: testGuildID, OwnerID: testUserID, Name: "intro"}
}

func TestAgentToolsExposesExpectedTools(t *testing.T) {
	mod := newIntroModuleWithThread(t, false)
	tools := mod.AgentTools()
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		names = append(names, tl.Name)
		assert.True(t, tl.SkipPermission, "tool %s should skip permission", tl.Name)
	}
	assert.ElementsMatch(t, []string{
		"lookup_user_intro_metadata",
		"lookup_self_intro_metadata",
		"read_user_intro_content",
		"read_self_intro_content",
	}, names)
}

func TestLookupIntroMetadata(t *testing.T) {
	mod := newIntroModuleWithThread(t, true)

	res := mod.lookupIntroMetadata(testUserID)
	require.Equal(t, "found", res.Status)
	require.NotNil(t, res.Intro)
	assert.Equal(t, "https://discord.com/channels/guild1/700", res.Intro.URL)
	assert.Equal(t, "intro", res.Intro.Name)

	miss := mod.lookupIntroMetadata("nobody")
	assert.Equal(t, "not_found", miss.Status)
	assert.Nil(t, miss.Intro)
}

func TestBuildIntroContentResultFound(t *testing.T) {
	res := buildIntroContentResult(testIntroMeta(), "Hi I'm Bob from Toronto, into co-op shooters.", nil)
	require.Equal(t, "found", res.Status)
	assert.Equal(t, "Hi I'm Bob from Toronto, into co-op shooters.", res.Content)
	assert.False(t, res.Truncated)
	require.NotNil(t, res.Intro)
	assert.Equal(t, "https://discord.com/channels/guild1/700", res.Intro.URL)
}

func TestBuildIntroContentResultEmpty(t *testing.T) {
	res := buildIntroContentResult(testIntroMeta(), "   \n  ", nil)
	assert.Equal(t, "empty", res.Status)
	assert.Empty(t, res.Content)
	require.NotNil(t, res.Intro, "empty result should still carry metadata for linking")
}

func TestBuildIntroContentResultUnreadable(t *testing.T) {
	res := buildIntroContentResult(testIntroMeta(), "", errors.New("boom"))
	assert.Equal(t, "unreadable", res.Status)
	assert.NotEmpty(t, res.Note)
	require.NotNil(t, res.Intro, "unreadable result should still carry metadata for linking")
}

func TestBuildIntroContentResultNotFound(t *testing.T) {
	res := buildIntroContentResult(nil, "", nil)
	assert.Equal(t, "not_found", res.Status)
	assert.Nil(t, res.Intro)
}

func TestBuildIntroContentResultTruncates(t *testing.T) {
	long := strings.Repeat("a", maxIntroContentChars+500)
	res := buildIntroContentResult(testIntroMeta(), long, nil)
	require.Equal(t, "found", res.Status)
	assert.True(t, res.Truncated)
	assert.Equal(t, maxIntroContentChars, len([]rune(res.Content)))
}

func TestReadSelfIntroContentResolvesCaller(t *testing.T) {
	mod := newIntroModuleWithThread(t, true)
	const sessionID = "sess-self-content"
	agentctx.Register(sessionID, agentctx.Caller{UserID: testUserID})
	defer agentctx.Unregister(sessionID)

	// Caller resolves to the seeded thread; with no session the body is unreadable.
	res := mod.readSelfIntroContent(sessionID)
	assert.Equal(t, "unreadable", res.Status)
	require.NotNil(t, res.Intro)

	miss := mod.readSelfIntroContent("unknown-session")
	assert.Equal(t, "not_found", miss.Status)
	assert.Equal(t, "no caller in session", miss.Note)
}

func TestLookupSelfIntroMetadata(t *testing.T) {
	mod := newIntroModuleWithThread(t, true)
	const sessionID = "sess-self-meta"
	agentctx.Register(sessionID, agentctx.Caller{UserID: testUserID})
	defer agentctx.Unregister(sessionID)

	res := mod.lookupSelfIntroMetadata(sessionID)
	require.Equal(t, "found", res.Status)
	require.NotNil(t, res.Intro)
	assert.Equal(t, "https://discord.com/channels/guild1/700", res.Intro.URL)

	miss := mod.lookupSelfIntroMetadata("unknown-session")
	assert.Equal(t, "not_found", miss.Status)
	assert.Equal(t, "no caller in session", miss.Note)
}

func TestCapIntroContent(t *testing.T) {
	short := "hello"
	got, truncated := capIntroContent(short)
	assert.Equal(t, short, got)
	assert.False(t, truncated)

	// Multi-byte runes under the rune cap but over the byte cap stay intact.
	multibyte := strings.Repeat("é", maxIntroContentChars-1)
	got, truncated = capIntroContent(multibyte)
	assert.Equal(t, multibyte, got)
	assert.False(t, truncated)
}
