package lfg

import (
	"testing"

	"gamerpal/internal/agentctx"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/forumcache"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testGuildID      = "guild1"
	testForumID      = "forum1"
	testCreateChanID = "create-thread-1"
)

func newLFGModuleForBatchTests(t *testing.T, includeCreateThreadChannel bool) *Module {
	t.Helper()
	cfg, fc := forumcache.NewTestForumCache(map[string]any{
		"gamerpals_lfg_forum_channel_id": testForumID,
	})
	fc.RegisterForum(testForumID)

	// Seed cache with one known game thread.
	fc.OnThreadCreate(nil, &discordgo.ThreadCreate{Channel: &discordgo.Channel{
		ID:       "thread-destiny2",
		ParentID: testForumID,
		GuildID:  testGuildID,
		OwnerID:  "owner1",
		Name:     "destiny-2",
	}})

	state := discordgo.NewState()
	guild := &discordgo.Guild{
		ID: testGuildID,
	}
	if includeCreateThreadChannel {
		guild.Channels = append(guild.Channels, &discordgo.Channel{
			ID:      testCreateChanID,
			GuildID: testGuildID,
			Name:    "create-a-thread",
			Type:    discordgo.ChannelTypeGuildText,
		})
	}
	require.NoError(t, state.GuildAdd(guild))
	require.NoError(t, state.ChannelAdd(&discordgo.Channel{
		ID:       "thread-destiny2",
		ParentID: testForumID,
		GuildID:  testGuildID,
		Name:     "destiny-2",
		Type:     discordgo.ChannelTypeGuildPublicThread,
	}))

	sess := &discordgo.Session{State: state}
	deps := &types.Dependencies{Config: cfg, ForumCache: fc, Session: sess}
	return New(deps)
}

func TestBatchSearchGameThreadsFoundAndMissingWithMention(t *testing.T) {
	m := newLFGModuleForBatchTests(t, true)
	const sessionID = "sess-batch-found-missing"
	agentctx.Register(sessionID, agentctx.Caller{GuildID: testGuildID})
	defer agentctx.Unregister(sessionID)

	result := m.batchSearchGameThreads([]string{"Destiny 2", "Warframe"}, sessionID)
	require.Len(t, result.Games, 1)
	assert.Equal(t, "Destiny 2", result.Games[0].GameName)
	require.NotNil(t, result.Games[0].Thread)
	assert.Contains(t, result.Games[0].Thread.URL, "thread-destiny2")
	assert.Equal(t, []string{"Warframe"}, result.MissingGames)
	assert.Contains(t, result.Note, "<#"+testCreateChanID+">")
}

func TestBatchSearchGameThreadsMissingFallbackNote(t *testing.T) {
	m := newLFGModuleForBatchTests(t, false)
	const sessionID = "sess-batch-fallback"
	agentctx.Register(sessionID, agentctx.Caller{GuildID: testGuildID})
	defer agentctx.Unregister(sessionID)

	result := m.batchSearchGameThreads([]string{"Warframe"}, sessionID)
	assert.Empty(t, result.Games)
	assert.Equal(t, []string{"Warframe"}, result.MissingGames)
	assert.Contains(t, result.Note, "#create-a-thread")
	assert.NotContains(t, result.Note, "<#")
}

func TestBatchSearchGameThreadsForumNotConfigured(t *testing.T) {
	cfg, fc := forumcache.NewTestForumCache(map[string]any{})
	sess := &discordgo.Session{State: discordgo.NewState()}
	m := New(&types.Dependencies{Config: cfg, ForumCache: fc, Session: sess})

	result := m.batchSearchGameThreads([]string{"Destiny 2"}, "sess-any")
	assert.Empty(t, result.Games)
	assert.Empty(t, result.MissingGames)
	assert.Equal(t, "lfg forum not configured", result.Note)
}

func TestBatchSearchGameThreadsEmptyInput(t *testing.T) {
	m := newLFGModuleForBatchTests(t, true)
	result := m.batchSearchGameThreads(nil, "sess-empty")
	assert.Empty(t, result.Games)
	assert.Empty(t, result.MissingGames)
	assert.Empty(t, result.Note)
}
