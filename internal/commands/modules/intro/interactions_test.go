package intro

import (
	"testing"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/forumcache"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockComponentAgent struct {
	reply      string
	gotPrompt  string
}

func (m *mockComponentAgent) HandleInternal(_ *discordgo.Session, prompt string) string {
	m.gotPrompt = prompt
	return m.reply
}

type componentHookCapture struct {
	responds         int
	edits            int
	lastEdit         string
	lastResponse     string
	lastFlags        discordgo.MessageFlags
	lastResponseType discordgo.InteractionResponseType
	lastComponents   []discordgo.MessageComponent
	componentsSet    bool
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
		}
		return nil
	}
	introEdit = func(_ *discordgo.Session, _ *discordgo.Interaction, edit *discordgo.WebhookEdit) (*discordgo.Message, error) {
		cap.edits++
		if edit != nil && edit.Content != nil {
			cap.lastEdit = *edit.Content
		}
		if edit != nil && edit.Components != nil {
			cap.componentsSet = true
			cap.lastComponents = *edit.Components
		}
		return nil, nil
	}
	defer func() {
		introRespond, introEdit = origRespond, origEdit
	}()
	fn()
}

func buildLookupInteraction(customID, guildID, channelID, userID, originalContent string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type:      discordgo.InteractionMessageComponent,
			GuildID:   guildID,
			ChannelID: channelID,
			Member:    &discordgo.Member{User: &discordgo.User{ID: userID}},
			Message:   &discordgo.Message{Content: originalContent},
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
		reply: `{"game-threads":[{"name":"Destiny 2","url":"https://discord.com/channels/guild/100","status":"found"},{"name":"Warframe","url":"","status":"not found"}]}`,
	}
	m := newIntroModuleForComponents(t, agent)
	cap := &componentHookCapture{}

	withComponentHooks(t, cap, func() {
		i := buildLookupInteraction(lookupGameThreadsCustomID, "guild1", "thread1", "user42", "💥 Intro text here")
		m.HandleComponent(&discordgo.Session{}, i)
	})

	require.Equal(t, 1, cap.responds)
	require.Equal(t, 1, cap.edits)
	assert.Equal(t, discordgo.InteractionResponseDeferredMessageUpdate, cap.lastResponseType)
	assert.Equal(t, "Find the game threads for the games <@user42> plays.", agent.gotPrompt)
	assert.True(t, cap.componentsSet)
	assert.Len(t, cap.lastComponents, 0)
	assert.Contains(t, cap.lastEdit, "💥 Intro text here")
	assert.Contains(t, cap.lastEdit, "\n\n---\n\n")
	assert.Contains(t, cap.lastEdit, "**Game Threads:**")
	assert.Contains(t, cap.lastEdit, "- **Destiny 2**: https://discord.com/channels/guild/100")
	assert.Contains(t, cap.lastEdit, "- **Warframe**: _not found_")
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
		i := buildLookupInteraction(lookupGameThreadsCustomID, "guild1", "thread1", "user42", "💥 Intro text here")
		m.HandleComponent(&discordgo.Session{}, i)
	})

	require.Equal(t, 1, cap.responds)
	require.Equal(t, 1, cap.edits)
	assert.Equal(t, discordgo.InteractionResponseDeferredMessageUpdate, cap.lastResponseType)
	assert.True(t, cap.componentsSet)
	assert.Len(t, cap.lastComponents, 0)
	assert.Contains(t, cap.lastEdit, "💥 Intro text here")
	assert.Contains(t, cap.lastEdit, "\n\n---\n\n")
	assert.Contains(t, cap.lastEdit, "not valid JSON")
}

func TestHandleLookupGamesComponentHandlesEmptyAgentResponse(t *testing.T) {
	m := newIntroModuleForComponents(t, &mockComponentAgent{reply: " "})
	cap := &componentHookCapture{}

	withComponentHooks(t, cap, func() {
		i := buildLookupInteraction(lookupGameThreadsCustomID, "guild1", "thread1", "user42", "💥 Intro text here")
		m.HandleComponent(&discordgo.Session{}, i)
	})

	require.Equal(t, 1, cap.responds)
	require.Equal(t, 1, cap.edits)
	assert.Equal(t, discordgo.InteractionResponseDeferredMessageUpdate, cap.lastResponseType)
	assert.True(t, cap.componentsSet)
	assert.Len(t, cap.lastComponents, 0)
	assert.Contains(t, cap.lastEdit, "💥 Intro text here")
	assert.Contains(t, cap.lastEdit, "\n\n---\n\n")
	assert.Contains(t, cap.lastEdit, "Failed to look up game threads")
}

func TestHandleComponentIgnoresOtherButtons(t *testing.T) {
	m := newIntroModuleForComponents(t, &mockComponentAgent{})
	cap := &componentHookCapture{}

	withComponentHooks(t, cap, func() {
		i := buildLookupInteraction("intro:other-button", "guild1", "thread1", "user42", "💥 Intro text here")
		m.HandleComponent(&discordgo.Session{}, i)
	})

	assert.Equal(t, 0, cap.responds)
	assert.Equal(t, 0, cap.edits)
}

func TestLookupGameThreadsMessageStringIncludesPreambleAndMarkdown(t *testing.T) {
	msg := gameThreadsMessage{
		Preamble: "💥 Intro text here",
		Result: []gameThread{
				{Name: "Destiny 2", URL: "https://discord.com/channels/guild/100", Status: "found"},
				{Name: "Warframe", URL: "", Status: "not found"},
		},
	}

	got := msg.String()

	assert.Contains(t, got, "💥 Intro text here")
	assert.Contains(t, got, "\n\n---\n\n")
	assert.Contains(t, got, "**Game Threads:**")
	assert.Contains(t, got, "- **Destiny 2**: https://discord.com/channels/guild/100")
	assert.Contains(t, got, "- **Warframe**: _not found_")
}

func TestLookupGameThreadsMessageBuildWithError(t *testing.T) {
	msg := gameThreadsMessage{
		Preamble: "💥 Intro text here",
	}

	got := msg.BuildWithError("❌ Lookup agent is unavailable.")

	assert.Equal(t, "💥 Intro text here\n\n---\n\n❌ Lookup agent is unavailable.", got)
}
