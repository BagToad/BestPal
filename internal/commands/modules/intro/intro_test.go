package intro

import (
	"testing"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/forumcache"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// local helper: construct forum cache + config inline (no shared testutil file)
func newTestForumCache(kv map[string]interface{}) (*config.Config, *forumcache.Service) {
	if kv == nil {
		kv = map[string]interface{}{"bot_token": "x"}
	}
	if _, ok := kv["bot_token"]; !ok {
		kv["bot_token"] = "x"
	}
	cfg := config.NewMockConfig(kv)
	return cfg, forumcache.New(cfg)
}

// hookCapture collects calls made via overridable hook functions.
type hookCapture struct {
	responds             int
	edits                int
	lastEdit             string
	logs                 []string
	lastRespondEphemeral []bool // track whether each respond used ephemeral flag
}

// buildInteraction constructs minimal interaction with guild + member.
func buildInteraction(guildID, userID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type:    discordgo.InteractionApplicationCommand, // required for ApplicationCommandData access
			GuildID: guildID,
			Member:  &discordgo.Member{User: &discordgo.User{ID: userID, Username: "user"}},
			Data: discordgo.ApplicationCommandInteractionData{ // minimal command data to satisfy handler
				Name: "intro",
			},
		},
	}
}

// seedThread inserts a thread for cache hit scenario.
func seedThread(fc *forumcache.Service, forumID, guildID, userID, threadID string) {
	fc.OnThreadCreate(nil, &discordgo.ThreadCreate{Channel: &discordgo.Channel{ID: threadID, ParentID: forumID, GuildID: guildID, OwnerID: userID, Name: "intro"}})
}

func withHooks(t *testing.T, cap *hookCapture, fn func()) {
	t.Helper()
	origRespond, origEdit, origLog := introRespond, introEdit, introLog
	introRespond = func(_ *discordgo.Session, _ *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
		cap.responds++
		if resp != nil && resp.Data != nil && (resp.Data.Flags&discordgo.MessageFlagsEphemeral) != 0 {
			cap.lastRespondEphemeral = append(cap.lastRespondEphemeral, true)
		} else {
			cap.lastRespondEphemeral = append(cap.lastRespondEphemeral, false)
		}
		return nil
	}
	introEdit = func(_ *discordgo.Session, _ *discordgo.Interaction, edit *discordgo.WebhookEdit) (*discordgo.Message, error) {
		cap.edits++
		if edit != nil && edit.Content != nil {
			cap.lastEdit = *edit.Content
		}
		return nil, nil
	}
	introLog = func(_ *types.Dependencies, _ *discordgo.Session, msg string) error {
		cap.logs = append(cap.logs, msg)
		return nil
	}
	defer func() { introRespond, introEdit, introLog = origRespond, origEdit, origLog }()
	fn()
}

func TestIntroCacheHitDefaultEphemeral(t *testing.T) {
	cfg, fc := newTestForumCache(map[string]interface{}{"gamerpals_introductions_forum_channel_id": "forumA"})
	fc.RegisterForum("forumA")
	seedThread(fc, "forumA", "guild1", "user1", "700")
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	mod := New(deps)
	cmds := map[string]*types.Command{}
	mod.Register(cmds, deps)
	cap := &hookCapture{}
	withHooks(t, cap, func() {
		inter := buildInteraction("guild1", "user1") // no ephemeral option -> default true
		mod.handleIntroSlash(&discordgo.Session{}, inter)
	})
	require.Equal(t, 1, cap.edits)
	assert.Contains(t, cap.lastEdit, "https://discord.com/channels/guild1/700")
	require.Len(t, cap.logs, 1)
	assert.Contains(t, cap.logs[0], "[IntroLookupSuccess]")
	assert.Contains(t, cap.logs[0], "ThreadID: 700")
	assert.Contains(t, cap.logs[0], "Forum: forumA")
	require.GreaterOrEqual(t, len(cap.lastRespondEphemeral), 1)
	assert.True(t, cap.lastRespondEphemeral[0], "default response should be ephemeral")
}

func TestIntroCacheMissExplicitNonEphemeral(t *testing.T) {
	cfg, fc := newTestForumCache(map[string]interface{}{"gamerpals_introductions_forum_channel_id": "forumB", "gamerpals_log_channel_id": "logChan"})
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	mod := New(deps)
	cmds := map[string]*types.Command{}
	mod.Register(cmds, deps)
	cap := &hookCapture{}
	withHooks(t, cap, func() {
		inter := &discordgo.InteractionCreate{
			Interaction: &discordgo.Interaction{
				Type:    discordgo.InteractionApplicationCommand,
				GuildID: "guild2",
				Member:  &discordgo.Member{User: &discordgo.User{ID: "userX", Username: "userX"}},
				Data: discordgo.ApplicationCommandInteractionData{
					Name: "intro",
					Options: []*discordgo.ApplicationCommandInteractionDataOption{
						{Name: "ephemeral", Type: discordgo.ApplicationCommandOptionBoolean, Value: false},
					},
				},
			},
		}
		mod.handleIntroSlash(&discordgo.Session{}, inter)
	})
	require.Equal(t, 1, cap.edits)
	assert.Contains(t, cap.lastEdit, "No introduction post found")
	require.Len(t, cap.logs, 1)
	assert.Contains(t, cap.logs[0], "[IntroLookupFailure]")
	assert.Contains(t, cap.logs[0], "Reason: cache_miss")
	assert.Contains(t, cap.logs[0], "Forum: forumB")
	require.GreaterOrEqual(t, len(cap.lastRespondEphemeral), 1)
	assert.False(t, cap.lastRespondEphemeral[0], "response should be non-ephemeral when option false")
}

func TestIntroConfigMissingDefaultEphemeral(t *testing.T) {
	cfg, fc := newTestForumCache(map[string]interface{}{}) // no forum id
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	mod := New(deps)
	cmds := map[string]*types.Command{}
	mod.Register(cmds, deps)
	cap := &hookCapture{}
	withHooks(t, cap, func() {
		inter := buildInteraction("guild3", "userZ")
		mod.handleIntroSlash(&discordgo.Session{}, inter)
	})
	// Should respond but not edit (since early return) and log failure.
	assert.Equal(t, 1, cap.responds)
	assert.Equal(t, 0, cap.edits)
	require.Len(t, cap.logs, 1)
	assert.Contains(t, cap.logs[0], "[IntroLookupFailure]")
	assert.Contains(t, cap.logs[0], "Reason: channel_not_configured")
	require.Len(t, cap.lastRespondEphemeral, 1)
	assert.True(t, cap.lastRespondEphemeral[0], "config missing error should honor default ephemeral")
}

// buildUserContextInteraction builds a minimal user context command interaction selecting targetUserID.
func buildUserContextInteraction(guildID, invokingUserID, targetUserID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type:    discordgo.InteractionApplicationCommand,
			GuildID: guildID,
			Member:  &discordgo.Member{User: &discordgo.User{ID: invokingUserID, Username: "invoker"}},
			Data: discordgo.ApplicationCommandInteractionData{
				Name:     "Lookup intro",
				TargetID: targetUserID,
				Resolved: &discordgo.ApplicationCommandInteractionDataResolved{Users: map[string]*discordgo.User{targetUserID: {ID: targetUserID, Username: "target"}}},
			},
		},
	}
}

func TestUserIntroCacheHitAlwaysEphemeral(t *testing.T) {
	cfg, fc := newTestForumCache(map[string]interface{}{"gamerpals_introductions_forum_channel_id": "forumUC"})
	fc.RegisterForum("forumUC")
	seedThread(fc, "forumUC", "guildUC", "targetUser", "900")
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	mod := New(deps)
	cmds := map[string]*types.Command{}
	mod.Register(cmds, deps)
	cap := &hookCapture{}
	withHooks(t, cap, func() {
		inter := buildUserContextInteraction("guildUC", "invoker1", "targetUser")
		mod.handleIntroUserContext(&discordgo.Session{}, inter)
	})
	require.Equal(t, 1, cap.edits)
	assert.Contains(t, cap.lastEdit, "https://discord.com/channels/guildUC/900")
	require.Len(t, cap.logs, 1)
	assert.Contains(t, cap.logs[0], "[IntroLookupSuccess]")
	assert.Contains(t, cap.logs[0], "ThreadID: 900")
	assert.Contains(t, cap.logs[0], "Forum: forumUC")
	// First respond should be ephemeral
	require.GreaterOrEqual(t, len(cap.lastRespondEphemeral), 1)
	assert.True(t, cap.lastRespondEphemeral[0])
}

func TestUserIntroCacheMissAlwaysEphemeral(t *testing.T) {
	cfg, fc := newTestForumCache(map[string]interface{}{"gamerpals_introductions_forum_channel_id": "forumUM", "gamerpals_log_channel_id": "logChan"})
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	mod := New(deps)
	cmds := map[string]*types.Command{}
	mod.Register(cmds, deps)
	cap := &hookCapture{}
	withHooks(t, cap, func() {
		inter := buildUserContextInteraction("guildUM", "invoker2", "missingUser")
		mod.handleIntroUserContext(&discordgo.Session{}, inter)
	})
	require.Equal(t, 1, cap.edits)
	assert.Contains(t, cap.lastEdit, "No introduction post found")
	require.Len(t, cap.logs, 1)
	assert.Contains(t, cap.logs[0], "[IntroLookupFailure]")
	assert.Contains(t, cap.logs[0], "Reason: cache_miss")
	assert.Contains(t, cap.logs[0], "Forum: forumUM")
	require.GreaterOrEqual(t, len(cap.lastRespondEphemeral), 1)
	assert.True(t, cap.lastRespondEphemeral[0])
}

func TestUserIntroConfigMissingAlwaysEphemeral(t *testing.T) {
	cfg, fc := newTestForumCache(map[string]interface{}{})
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	mod := New(deps)
	cmds := map[string]*types.Command{}
	mod.Register(cmds, deps)
	cap := &hookCapture{}
	withHooks(t, cap, func() {
		inter := buildUserContextInteraction("guildNX", "invoker3", "targetNX")
		mod.handleIntroUserContext(&discordgo.Session{}, inter)
	})
	assert.Equal(t, 1, cap.responds)
	assert.Equal(t, 0, cap.edits)
	require.Len(t, cap.logs, 1)
	assert.Contains(t, cap.logs[0], "[IntroLookupFailure]")
	assert.Contains(t, cap.logs[0], "Reason: channel_not_configured")
	require.Len(t, cap.lastRespondEphemeral, 1)
	assert.True(t, cap.lastRespondEphemeral[0])
}
