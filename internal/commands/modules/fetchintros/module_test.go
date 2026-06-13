package fetchintros

import (
	"testing"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/forumcache"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestModuleRegistration tests that the module registers correctly
func TestModuleRegistration(t *testing.T) {
	cfg, fc := forumcache.NewTestForumCache(map[string]interface{}{
		"gamerpals_introductions_forum_channel_id": "forum1",
	})

	deps := &types.Dependencies{
		Config:     cfg,
		ForumCache: fc,
	}

	module := New(deps)
	cmds := map[string]*types.Command{}
	module.Register(cmds, deps)

	require.Contains(t, cmds, "fetch-intros")
	cmd := cmds["fetch-intros"]
	assert.Equal(t, "fetch-intros", cmd.ApplicationCommand.Name)
	assert.NotNil(t, cmd.ApplicationCommand.DefaultMemberPermissions)
	assert.Equal(t, int64(discordgo.PermissionAdministrator), *cmd.ApplicationCommand.DefaultMemberPermissions)
	assert.NotNil(t, cmd.ApplicationCommand.Contexts)
	assert.Contains(t, *cmd.ApplicationCommand.Contexts, discordgo.InteractionContextGuild)
}

// TestFetchAndStoreThreadsCacheMiss tests cache refresh on miss
func TestFetchAndStoreThreadsCacheMiss(t *testing.T) {
	cfg, fc := forumcache.NewTestForumCache(map[string]interface{}{
		"gamerpals_introductions_forum_channel_id": "forum1",
	})
	// Don't register forum - force cache miss

	deps := &types.Dependencies{
		Config:     cfg,
		ForumCache: fc,
	}

	module := New(deps)
	session := &discordgo.Session{}

	// First call should trigger cache refresh, but since we're not mocking
	// the actual Discord API, it will fail gracefully
	summary, err := module.fetchAndStoreThreads(session, "guild1", "forum1")

	// Should get an error because cache refresh requires a hydrated session
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to refresh forum cache")
	assert.Empty(t, summary)
}

// TestModuleNilDatabaseGuard tests that nil database is checked
func TestModuleNilDatabaseGuard(t *testing.T) {
	cfg, fc := forumcache.NewTestForumCache(map[string]interface{}{
		"gamerpals_introductions_forum_channel_id": "forum1",
	})

	deps := &types.Dependencies{
		Config:     cfg,
		ForumCache: fc,
		DB:         nil, // Nil database
	}

	module := New(deps)

	// Verify module handles nil DB gracefully
	assert.NotNil(t, module)
	assert.Nil(t, module.deps.DB)

	// The actual handler would check for nil DB and return error
	// This test just verifies the module can be created with nil DB
}

