package intro

import (
	"testing"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/forumcache"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hookCaptureInteraction collects calls made via hook functions for interactions
type hookCaptureInteraction struct {
	responds    int
	edits       int
	lastEdit    string
	lastRespondEphemeral []bool
	logs        []string
}

func buildComponentInteraction(guildID, threadID, userID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type:    discordgo.InteractionMessageComponent,
			GuildID: guildID,
			Member:  &discordgo.Member{User: &discordgo.User{ID: userID, Username: "user"}},
			Data: discordgo.MessageComponentInteractionData{
				CustomID: "intro_lookup_games::" + threadID + "::" + userID,
			},
		},
	}
}

func withHooksInteraction(t *testing.T, cap *hookCaptureInteraction, fn func()) {
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

func TestHandleComponentValidButtonClick(t *testing.T) {
	cfg, fc := forumcache.NewTestForumCache(map[string]any{})
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	m := New(deps)

	cap := &hookCaptureInteraction{}
	withHooksInteraction(t, cap, func() {
		inter := buildComponentInteraction("guild1", "thread123", "user456")
		m.HandleComponent(&discordgo.Session{}, inter)
	})

	// Should have deferred the response and edited it
	require.Equal(t, 1, cap.responds)
	require.Equal(t, 1, cap.edits)
	assert.Contains(t, cap.lastEdit, "Looking up game threads")
}

func TestHandleComponentInvalidCustomID(t *testing.T) {
	cfg, fc := forumcache.NewTestForumCache(map[string]any{})
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	m := New(deps)

	cap := &hookCaptureInteraction{}
	withHooksInteraction(t, cap, func() {
		inter := &discordgo.InteractionCreate{
			Interaction: &discordgo.Interaction{
				Type:    discordgo.InteractionMessageComponent,
				GuildID: "guild1",
				Member:  &discordgo.Member{User: &discordgo.User{ID: "user1"}},
				Data: discordgo.MessageComponentInteractionData{
					CustomID: "intro_lookup_games::onlythread", // Invalid format
				},
			},
		}
		m.HandleComponent(&discordgo.Session{}, inter)
	})

	// Should respond with error
	require.Equal(t, 1, cap.responds)
	assert.Contains(t, cap.lastEdit, "Invalid button data")
}

func TestHandleComponentNilData(t *testing.T) {
	cfg, fc := forumcache.NewTestForumCache(map[string]any{})
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	m := New(deps)

	// Should not panic on nil data
	m.HandleComponent(&discordgo.Session{}, nil)
	m.HandleComponent(&discordgo.Session{}, &discordgo.InteractionCreate{})
}

func TestHandleComponentWrongPrefix(t *testing.T) {
	cfg, fc := forumcache.NewTestForumCache(map[string]any{})
	deps := &types.Dependencies{Config: cfg, ForumCache: fc}
	m := New(deps)

	cap := &hookCaptureInteraction{}
	withHooksInteraction(t, cap, func() {
		inter := &discordgo.InteractionCreate{
			Interaction: &discordgo.Interaction{
				Type:    discordgo.InteractionMessageComponent,
				GuildID: "guild1",
				Member:  &discordgo.Member{User: &discordgo.User{ID: "user1"}},
				Data: discordgo.MessageComponentInteractionData{
					CustomID: "some_other_button::data",
				},
			},
		}
		m.HandleComponent(&discordgo.Session{}, inter)
	})

	// Should not respond to non-intro buttons
	require.Equal(t, 0, cap.responds)
}
