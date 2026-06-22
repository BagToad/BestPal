package intro

import (
	"errors"
	"testing"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/forumcache"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockComponentAgent struct {
	reply      string
	err        error
	gotPrompt  string
	gotChannel string
}

func (m *mockComponentAgent) HandleComponent(_ *discordgo.Session, i *discordgo.InteractionCreate, prompt string) (string, error) {
	m.gotPrompt = prompt
	if i != nil {
		m.gotChannel = i.ChannelID
	}
	if m.err != nil {
		return "", m.err
	}
	return m.reply, nil
}

type componentHookCapture struct {
	responds     int
	edits        int
	lastEdit     string
	lastResponse string
	lastFlags    discordgo.MessageFlags
}

func withComponentHooks(t *testing.T, cap *componentHookCapture, fn func()) {
	t.Helper()
	origRespond, origEdit := introRespond, introEdit
	introRespond = func(_ *discordgo.Session, _ *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
		cap.responds++
		if resp != nil && resp.Data != nil {
			cap.lastFlags = resp.Data.Flags
			cap.lastResponse = resp.Data.Content
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
	defer func() {
		introRespond, introEdit = origRespond, origEdit
	}()
	fn()
}

func buildLookupInteraction(customID, guildID, channelID, userID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type:      discordgo.InteractionMessageComponent,
			GuildID:   guildID,
			ChannelID: channelID,
			Member:    &discordgo.Member{User: &discordgo.User{ID: userID}},
			Data: discordgo.MessageComponentInteractionData{
				CustomID: customID,
			},
		},
	}
}

func newIntroModuleForComponents(t *testing.T, agent types.ComponentAgent) *Module {
	t.Helper()
	cfg, fc := forumcache.NewTestForumCache(map[string]any{})
	deps := &types.Dependencies{Config: cfg, ForumCache: fc, Agent: agent}
	m := New(deps)
	m.Register(map[string]*types.Command{}, deps)
	return m
}

func TestHandleLookupGamesComponentBuildsDeterministicMarkdown(t *testing.T) {
	agent := &mockComponentAgent{
		reply: `{"games":[{"game_name":"Destiny 2","thread":{"name":"destiny-2","url":"https://discord.com/channels/guild/100"}}],"missing_games":["Warframe"],"note":"ℹ️ Missing a thread? Create one in <#create-thread-id>."}`,
	}
	m := newIntroModuleForComponents(t, agent)
	cap := &componentHookCapture{}

	withComponentHooks(t, cap, func() {
		i := buildLookupInteraction(lookupGameThreadsCustomID, "guild1", "thread1", "user42")
		m.HandleComponent(&discordgo.Session{}, i)
	})

	require.Equal(t, 1, cap.responds)
	require.Equal(t, 1, cap.edits)
	assert.Equal(t, "Find the game threads for the games <@user42> plays.", agent.gotPrompt)
	assert.Equal(t, "thread1", agent.gotChannel)
	assert.Contains(t, cap.lastEdit, "**Game Threads:**")
	assert.Contains(t, cap.lastEdit, "- **Destiny 2**: [destiny-2](https://discord.com/channels/guild/100)")
	assert.Contains(t, cap.lastEdit, "Create one in <#create-thread-id>.")
}

func TestHandleLookupGamesComponentRejectsMissingUser(t *testing.T) {
	m := newIntroModuleForComponents(t, &mockComponentAgent{})
	cap := &componentHookCapture{}
	i := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type:      discordgo.InteractionMessageComponent,
			GuildID:   "guild1",
			ChannelID: "thread1",
			Data:      discordgo.MessageComponentInteractionData{CustomID: lookupGameThreadsCustomID},
		},
	}

	withComponentHooks(t, cap, func() {
		m.HandleComponent(&discordgo.Session{}, i)
	})

	require.Equal(t, 1, cap.responds)
	require.Equal(t, 0, cap.edits)
	assert.Contains(t, cap.lastResponse, "Could not determine user identity")
	assert.Equal(t, discordgo.MessageFlagsEphemeral, cap.lastFlags)
}

func TestHandleLookupGamesComponentHandlesInvalidJSON(t *testing.T) {
	m := newIntroModuleForComponents(t, &mockComponentAgent{reply: "not-json"})
	cap := &componentHookCapture{}

	withComponentHooks(t, cap, func() {
		i := buildLookupInteraction(lookupGameThreadsCustomID, "guild1", "thread1", "user42")
		m.HandleComponent(&discordgo.Session{}, i)
	})

	require.Equal(t, 1, cap.responds)
	require.Equal(t, 1, cap.edits)
	assert.Contains(t, cap.lastEdit, "not valid JSON")
}

func TestHandleLookupGamesComponentHandlesAgentError(t *testing.T) {
	m := newIntroModuleForComponents(t, &mockComponentAgent{err: errors.New("boom")})
	cap := &componentHookCapture{}

	withComponentHooks(t, cap, func() {
		i := buildLookupInteraction(lookupGameThreadsCustomID, "guild1", "thread1", "user42")
		m.HandleComponent(&discordgo.Session{}, i)
	})

	require.Equal(t, 1, cap.responds)
	require.Equal(t, 1, cap.edits)
	assert.Contains(t, cap.lastEdit, "Failed to look up game threads")
}

func TestHandleComponentIgnoresOtherButtons(t *testing.T) {
	m := newIntroModuleForComponents(t, &mockComponentAgent{})
	cap := &componentHookCapture{}

	withComponentHooks(t, cap, func() {
		i := buildLookupInteraction("intro:other-button", "guild1", "thread1", "user42")
		m.HandleComponent(&discordgo.Session{}, i)
	})

	assert.Equal(t, 0, cap.responds)
	assert.Equal(t, 0, cap.edits)
}
