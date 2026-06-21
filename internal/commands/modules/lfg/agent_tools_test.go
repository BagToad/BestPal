package lfg

import (
	"testing"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/forumcache"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedGameThread inserts a game thread into the forum cache for testing
func seedGameThread(fc *forumcache.Service, forumID, guildID, gameName, threadID string) {
	fc.OnThreadCreate(nil, &discordgo.ThreadCreate{
		Channel: &discordgo.Channel{
			ID:       threadID,
			ParentID: forumID,
			GuildID:  guildID,
			OwnerID:  "bot",
			Name:     gameName,
		},
	})
}

func TestLFGBatchSearchMultipleGamesFound(t *testing.T) {
	cfg, fc := forumcache.NewTestForumCache(map[string]any{
		"gamerpals_lfg_forum_channel_id": "lfgForum",
	})
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	m := New(deps)

	fc.RegisterForum("lfgForum")
	seedGameThread(fc, "lfgForum", "guild1", "Dark Souls", "thread1")
	seedGameThread(fc, "lfgForum", "guild1", "Elden Ring", "thread2")

	tool := m.newLFGBatchSearchTool()
	require.NotNil(t, tool)

	result, err := tool.Func.(func(lfgBatchSearchParams, interface{}) (*batchSearchResult, error))(
		lfgBatchSearchParams{GameNames: []string{"Dark Souls", "Elden Ring"}},
		nil,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Games, 2)
	assert.Len(t, result.MissingGames, 0)
	assert.Equal(t, "Dark Souls", result.Games[0].GameName)
	assert.Equal(t, "Elden Ring", result.Games[1].GameName)
}

func TestLFGBatchSearchSomeMissing(t *testing.T) {
	cfg, fc := forumcache.NewTestForumCache(map[string]any{
		"gamerpals_lfg_forum_channel_id": "lfgForum",
	})
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	m := New(deps)

	fc.RegisterForum("lfgForum")
	seedGameThread(fc, "lfgForum", "guild1", "Dark Souls", "thread1")

	tool := m.newLFGBatchSearchTool()
	require.NotNil(t, tool)

	result, err := tool.Func.(func(lfgBatchSearchParams, interface{}) (*batchSearchResult, error))(
		lfgBatchSearchParams{GameNames: []string{"Dark Souls", "Nonexistent Game"}},
		nil,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Games, 1)
	assert.Len(t, result.MissingGames, 1)
	assert.Equal(t, "Dark Souls", result.Games[0].GameName)
	assert.Equal(t, "Nonexistent Game", result.MissingGames[0])
}

func TestLFGBatchSearchNoGamesFound(t *testing.T) {
	cfg, fc := forumcache.NewTestForumCache(map[string]any{
		"gamerpals_lfg_forum_channel_id": "lfgForum",
	})
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	m := New(deps)

	fc.RegisterForum("lfgForum")

	tool := m.newLFGBatchSearchTool()
	require.NotNil(t, tool)

	result, err := tool.Func.(func(lfgBatchSearchParams, interface{}) (*batchSearchResult, error))(
		lfgBatchSearchParams{GameNames: []string{"Nonexistent Game 1", "Nonexistent Game 2"}},
		nil,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Games, 0)
	assert.Len(t, result.MissingGames, 2)
}

func TestLFGBatchSearchEmptyInput(t *testing.T) {
	cfg, fc := forumcache.NewTestForumCache(map[string]any{
		"gamerpals_lfg_forum_channel_id": "lfgForum",
	})
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	m := New(deps)

	tool := m.newLFGBatchSearchTool()
	require.NotNil(t, tool)

	result, err := tool.Func.(func(lfgBatchSearchParams, interface{}) (*batchSearchResult, error))(
		lfgBatchSearchParams{GameNames: []string{}},
		nil,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "no games to search", result.Note)
}

func TestLFGBatchSearchForumNotConfigured(t *testing.T) {
	cfg := &config.Config{}
	cfg.SetGuildStore(&config.MockGuildStore{})
	deps := &types.Dependencies{Config: cfg}
	m := New(deps)

	tool := m.newLFGBatchSearchTool()
	require.NotNil(t, tool)

	result, err := tool.Func.(func(lfgBatchSearchParams, interface{}) (*batchSearchResult, error))(
		lfgBatchSearchParams{GameNames: []string{"Dark Souls"}},
		nil,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "lfg forum not configured", result.Note)
}
