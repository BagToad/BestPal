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

// hookCapture collects calls made via overridable hook functions.
type hookCapture struct {
	responds int
	edits    int
	lastEdit string
	logs     []string
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
	introRespond = func(_ *discordgo.Session, _ *discordgo.Interaction, _ *discordgo.InteractionResponse) error {
		cap.responds++
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

func TestIntroCacheHit(t *testing.T) {
	cfg := config.NewMockConfig(map[string]interface{}{"gamerpals_introductions_forum_channel_id": "forumA"})
	fc := forumcache.New()
	fc.RegisterForum("forumA")
	seedThread(fc, "forumA", "guild1", "user1", "700")
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	mod := New(deps)
	cmds := map[string]*types.Command{}
	mod.Register(cmds, deps)
	cap := &hookCapture{}
	withHooks(t, cap, func() {
		inter := buildInteraction("guild1", "user1")
		mod.handleIntro(&discordgo.Session{}, inter)
	})
	require.Equal(t, 1, cap.edits)
	assert.Contains(t, cap.lastEdit, "https://discord.com/channels/guild1/700")
	assert.Empty(t, cap.logs, "no log on hit")
}

func TestIntroCacheMiss(t *testing.T) {
	cfg := config.NewMockConfig(map[string]interface{}{"gamerpals_introductions_forum_channel_id": "forumB", "gamerpals_log_channel_id": "logChan"})
	fc := forumcache.New() // no threads seeded
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	mod := New(deps)
	cmds := map[string]*types.Command{}
	mod.Register(cmds, deps)
	cap := &hookCapture{}
	withHooks(t, cap, func() {
		inter := buildInteraction("guild2", "userX")
		mod.handleIntro(&discordgo.Session{}, inter)
	})
	require.Equal(t, 1, cap.edits)
	assert.Contains(t, cap.lastEdit, "No introduction post found")
	require.Len(t, cap.logs, 1)
	assert.Contains(t, cap.logs[0], "[IntroCacheMiss]")
	assert.Contains(t, cap.logs[0], "forum=forumB")
}

func TestIntroConfigMissing(t *testing.T) {
	cfg := config.NewMockConfig(map[string]interface{}{}) // no forum id
	fc := forumcache.New()
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	mod := New(deps)
	cmds := map[string]*types.Command{}
	mod.Register(cmds, deps)
	cap := &hookCapture{}
	withHooks(t, cap, func() {
		inter := buildInteraction("guild3", "userZ")
		mod.handleIntro(&discordgo.Session{}, inter)
	})
	// Should respond but not edit (since early return) and no logs.
	assert.Equal(t, 1, cap.responds)
	assert.Equal(t, 0, cap.edits)
	assert.Empty(t, cap.logs)
}
