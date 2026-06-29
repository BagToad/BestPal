package intro

import (
	"testing"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/database"
	"gamerpal/internal/forumcache"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockComponentAgent struct {
	reply     string
	gotPrompt string
}

func (m *mockComponentAgent) HandleInternal(_ *discordgo.Session, prompt string) string {
	m.gotPrompt = prompt
	return m.reply
}

type componentHookCapture struct {
	responds         int
	edits            int
	lastResponse     string
	lastFlags        discordgo.MessageFlags
	lastResponseType discordgo.InteractionResponseType
	lastComponents   []discordgo.MessageComponent
}

func withComponentHooks(t *testing.T, cap *componentHookCapture, fn func()) {
	t.Helper()
	origRespond, origEdit := introRespond, introEdit
	introRespond = func(_ *discordgo.Session, _ *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
		cap.responds++
		if resp != nil {
			cap.lastResponseType = resp.Type
		}
		if resp != nil && resp.Data != nil {
			cap.lastFlags = resp.Data.Flags
			cap.lastResponse = resp.Data.Content
			cap.lastComponents = resp.Data.Components
		}
		return nil
	}
	introEdit = func(_ *discordgo.Session, _ *discordgo.Interaction, edit *discordgo.WebhookEdit) (*discordgo.Message, error) {
		cap.edits++
		if edit != nil && edit.Components != nil {
			cap.lastComponents = *edit.Components
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
			Data:      discordgo.MessageComponentInteractionData{CustomID: customID},
		},
	}
}

func newIntroModuleForComponents(t *testing.T, agent types.ComponentAgent, db *database.DB) *Module {
	t.Helper()
	cfg, fc := forumcache.NewTestForumCache(map[string]any{
		"intro_feed_channel_id": "feed1",
	})
	deps := &types.Dependencies{Config: cfg, ForumCache: fc, Agent: agent, DB: db}
	m := New(deps)
	m.Register(map[string]*types.Command{}, deps)
	return m
}

func extractLookupButton(t *testing.T, components []discordgo.MessageComponent) discordgo.Button {
	t.Helper()
	require.NotEmpty(t, components)
	container, ok := components[0].(discordgo.Container)
	require.True(t, ok)
	for _, c := range container.Components {
		row, ok := c.(discordgo.ActionsRow)
		if !ok {
			continue
		}
		require.Len(t, row.Components, 1)
		button, ok := row.Components[0].(discordgo.Button)
		require.True(t, ok)
		return button
	}
	t.Fatalf("lookup button row not found")
	return discordgo.Button{}
}

func TestHandleLookupGamesComponentSuccessRendersState(t *testing.T) {
	agent := &mockComponentAgent{
		reply: `{"game-threads":[{"name":"Destiny 2","url":"https://discord.com/channels/guild/100","status":"found"}]}`,
	}
	m := newIntroModuleForComponents(t, agent, nil)
	cap := &componentHookCapture{}

	withComponentHooks(t, cap, func() {
		i := buildLookupInteraction(LookupGameThreadsCustomID, "guild1", "thread1", "user42")
		m.HandleComponent(&discordgo.Session{}, i)
	})

	require.Equal(t, 1, cap.responds)
	require.Equal(t, 1, cap.edits)
	assert.Equal(t, discordgo.InteractionResponseUpdateMessage, cap.lastResponseType)
	assert.Equal(t, "Find the game threads for the games <@user42> plays.", agent.gotPrompt)

	button := extractLookupButton(t, cap.lastComponents)
	assert.Equal(t, "Find your game threads", button.Label)
	assert.False(t, button.Disabled)
}

func TestHandleLookupGamesComponentResetsMessageOnAgentFailure(t *testing.T) {
	m := newIntroModuleForComponents(t, &mockComponentAgent{reply: ""}, nil)
	cap := &componentHookCapture{}

	withComponentHooks(t, cap, func() {
		i := buildLookupInteraction(LookupGameThreadsCustomID, "guild1", "thread1", "user42")
		m.HandleComponent(&discordgo.Session{}, i)
	})

	require.Equal(t, 2, cap.responds)
	require.Equal(t, 1, cap.edits)
	assert.Equal(t, discordgo.InteractionResponseChannelMessageWithSource, cap.lastResponseType)
	assert.Contains(t, cap.lastResponse, "Failed to look up game threads right now")
}

func TestHandleLookupGamesComponentRejectsMissingUser(t *testing.T) {
	m := newIntroModuleForComponents(t, &mockComponentAgent{}, nil)
	cap := &componentHookCapture{}
	i := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type:      discordgo.InteractionMessageComponent,
			GuildID:   "guild1",
			ChannelID: "thread1",
			Data:      discordgo.MessageComponentInteractionData{CustomID: LookupGameThreadsCustomID},
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

func TestHandleComponentIgnoresOtherButtons(t *testing.T) {
	m := newIntroModuleForComponents(t, &mockComponentAgent{}, nil)
	cap := &componentHookCapture{}

	withComponentHooks(t, cap, func() {
		i := buildLookupInteraction("intro:other-button", "guild1", "thread1", "user42")
		m.HandleComponent(&discordgo.Session{}, i)
	})

	assert.Equal(t, 0, cap.responds)
	assert.Equal(t, 0, cap.edits)
}
