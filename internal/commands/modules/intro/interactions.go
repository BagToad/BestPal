package intro

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"gamerpal/internal/agentctx"
	"gamerpal/internal/agentengine"
	"strings"

	"github.com/bwmarrin/discordgo"
)

//go:embed prompts/intro_system_prompt.md
var introSystemPromptRaw string
var introSystemPrompt = strings.TrimSpace(introSystemPromptRaw)

const LookupGameThreadsCustomID = "intro:lookup-games"

type GameThreadsAgentResult struct {
	GameThreads []GameThread `json:"game-threads"`
}

type GameThread struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// HandleComponent routes component interactions for the intro module.
func (m *Module) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil {
		return
	}

	cid := i.MessageComponentData().CustomID
	if strings.HasPrefix(cid, LookupGameThreadsCustomID) {
		m.handleLookupGamesComponent(s, i)
	}
}

// It handles the "Lookup Game Threads" button interaction
func (m *Module) handleLookupGamesComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.config == nil || m.config.Config == nil || m.config.DB == nil || m.config.Agent == nil {
		if m.config != nil && m.config.Config != nil {
			m.config.Config.Logger.Errorf("game threads lookup unavailable: missing dependency (channel=%s)", i.ChannelID)
		}
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Game Threads Lookup is unavailable.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if i.ChannelID == "" {
		m.config.Config.Logger.Errorf("game threads lookup failed: empty intro thread channel")
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Game Threads Lookup is unavailable.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Update the message to show that the lookup is in progress
	// Button label is updated and the button is disabled
	autoIntroComment := newAutoIntroComment(i.GuildID, m.config.Config.GetIntroFeedChannelID())
	autoIntroComment.aiLoadingState = true
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Flags:      discordgo.MessageFlagsIsComponentsV2,
			Components: autoIntroComment.components(),
		},
	})

	// Get the intro message from the intro thread where the interaction occurred.
	introMessage, err := s.ChannelMessage(i.ChannelID, i.ChannelID)
	if err != nil || introMessage == nil {
		m.config.Config.Logger.Errorf("game threads lookup failed: could not fetch intro post (thread=%s err=%v)", i.ChannelID, err)
		respondErrorWithComponentsReset(m, autoIntroComment, s, i, "❌ Failed to look up game threads right now. Please try again.")
		return
	}

	// Extract the user ID from the intro message's author.
	userID := introMessage.Author.ID
	if userID == "" {
		m.config.Config.Logger.Errorf("game threads lookup failed: intro post has no resolvable author (thread=%s)", i.ChannelID)
		respondErrorWithComponentsReset(m, autoIntroComment, s, i, "❌ Failed to look up game threads right now. Please try again.")
		return
	}

	// Eligibility check: if the intro post has been edited since the last game threads lookup,
	// we can proceed with the lookup. Otherwise, we inform the user that no changes have been made

	// Get the timestamp of the last edit to the intro message
	introEditedAt, err := discordgo.SnowflakeTimestamp(introMessage.ID)
	if err != nil {
		m.config.Config.Logger.Errorf("game threads lookup failed: invalid intro message snowflake (thread=%s message=%s err=%v)", i.ChannelID, introMessage.ID, err)
		respondErrorWithComponentsReset(m, autoIntroComment, s, i, "❌ Failed to look up game threads right now. Please try again.")
		return
	}
	if introMessage.EditedTimestamp != nil {
		introEditedAt = *introMessage.EditedTimestamp
	}

	eligible, _, err := m.config.DB.IsIntroEligibleForGameThreadsLookup(i.ChannelID, introEditedAt)
	if err != nil {
		m.config.Config.Logger.Errorf("game threads lookup failed: eligibility check error (thread=%s err=%v)", i.ChannelID, err)
		respondErrorWithComponentsReset(m, autoIntroComment, s, i, "❌ Failed to look up game threads right now. Please try again.")
		return
	}
	if !eligible {
		m.config.Config.Logger.Errorf("game threads lookup skipped: intro unchanged since last run (thread=%s)", i.ChannelID)
		respondErrorWithComponentsReset(m, autoIntroComment, s, i, "❌ The intro post has no changes to reflect in the list of game threads.")
		return
	}

	// Call the agent to find the game threads for the user
	// If the agent reponds with an empty string,
	// 1. reset the auto post to the original state, and
	// 2. inform the user of the failure ephemerally
	prompt := fmt.Sprintf("Find the game threads for the games <@%s> plays.", userID)

	opts := agentengine.HandleInternalOptions{
		S:            s,
		SystemPrompt: introSystemPrompt,
		UserPrompt:   prompt,
		// Set the user that initiated the interaction (button click) as the caller for the agent request.
		Caller: agentctx.Caller{
			UserID:  i.Member.User.ID,
			GuildID: i.GuildID,
		},
	}

	jsonReply := m.config.Agent.HandleInternal(opts)
	if strings.TrimSpace(jsonReply) == "" {
		m.config.Config.Logger.Errorf("game threads lookup failed: agent returned empty response (thread=%s user=%s)", i.ChannelID, userID)
		respondErrorWithComponentsReset(m, autoIntroComment, s, i, "❌ Failed to look up game threads right now. Please try again.")
		return
	}

	// Parse the agent's JSON response into a structured result.
	// If failed to parse,
	// 1. reset the auto post to the original state, and
	// 2. inform the user of the failure ephemerally
	var agentResult GameThreadsAgentResult
	if err := json.Unmarshal([]byte(jsonReply), &agentResult); err != nil {
		m.config.Config.Logger.Errorf("game threads lookup failed: invalid agent response json (thread=%s user=%s err=%v)", i.ChannelID, userID, err)
		respondErrorWithComponentsReset(m, autoIntroComment, s, i, "❌ Failed to look up game threads right now. Please try again.")
		return
	}

	// Finding game threads succeeded, update the auto post to show the results.
	autoIntroComment.aiLoadingState = false
	autoIntroComment.gameThreads = agentResult.GameThreads
	finalComponents := autoIntroComment.components()
	_, err = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Components: &finalComponents})
	if err != nil {
		m.config.Config.Logger.Errorf("Error editing response: %v", err)
	}
	m.config.Config.Logger.Infof("game threads lookup completed (thread=%s user=%s results=%d)", i.ChannelID, userID, len(agentResult.GameThreads))

	// Track the execution timestamp of the game threads lookup for this intro thread in the database.
	if err := m.config.DB.UpsertGameThreadsLookupExecution(i.ChannelID); err != nil {
		m.config.Config.Logger.Errorf("failed to update game threads lookup execution tracker for intro thread %s: %v", i.ChannelID, err)
	}
}

func respondErrorWithComponentsReset(m *Module, autoIntroComment AutoIntroComment, s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	autoIntroComment.aiLoadingState = false
	resetComponents := autoIntroComment.components()
	_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Components: &resetComponents})
	if err != nil {
		m.config.Config.Logger.Errorf("Error editing response: %v", err)
	}
	_, err = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: message,
		Flags:   discordgo.MessageFlagsEphemeral,
	})
	if err != nil {
		m.config.Config.Logger.Errorf("Error sending follow-up message: %v", err)
	}
}
