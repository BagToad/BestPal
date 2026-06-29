package intro

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

const LookupGameThreadsCustomID = "intro:lookup-games"

type GameThreadsAgentResult struct {
	GameThreads []GameThreads `json:"game-threads"`
}

type GameThreads struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Status string `json:"status"`
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

func (m *Module) handleLookupGamesComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {

	if m.config == nil || m.config.Config == nil || m.config.DB == nil || m.config.Agent == nil {
		if m.config != nil && m.config.Config != nil {
			m.config.Config.Logger.Warnf("game threads lookup unavailable: missing dependency (channel=%s)", i.ChannelID)
		}
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Game Threads Lookup is unavailable.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if i.ChannelID == "" {
		m.config.Config.Logger.Warnf("game threads lookup failed: empty intro thread channel")
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Could not determine the intro thread channel.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	introMessage, err := s.ChannelMessage(i.ChannelID, i.ChannelID)
	if err != nil || introMessage == nil {
		m.config.Config.Logger.Warnf("game threads lookup failed: could not fetch intro post (thread=%s err=%v)", i.ChannelID, err)
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Could not find the intro post.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Extract the user ID from the intro message's author
	userID := ""
	if introMessage.Author != nil && introMessage.Author.ID != "" {
		userID = introMessage.Author.ID
	}
	if userID == "" && introMessage.Member != nil && introMessage.Member.User != nil {
		userID = introMessage.Member.User.ID
	}
	if userID == "" {
		m.config.Config.Logger.Warnf("game threads lookup failed: intro post has no resolvable author (thread=%s)", i.ChannelID)
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Could not determine the user ID from the intro post.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Eligibility check: if the intro post has been edited since the last game threads lookup,
	// we can proceed with the lookup. Otherwise, we inform the user that no changes have been made

	// Get the timestamp of the last edit to the intro message
	introEditedAt, err := discordgo.SnowflakeTimestamp(introMessage.ID)
	if err != nil {
		m.config.Config.Logger.Warnf("game threads lookup failed: invalid intro message snowflake (thread=%s message=%s err=%v)", i.ChannelID, introMessage.ID, err)
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Could not find the intro post.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}
	if introMessage.EditedTimestamp != nil {
		introEditedAt = *introMessage.EditedTimestamp
	}

	eligible, _, err := m.config.DB.IsIntroEligibleForGameThreadsLookup(i.ChannelID, introEditedAt)
	if err != nil {
		m.config.Config.Logger.Warnf("game threads lookup failed: eligibility check error (thread=%s err=%v)", i.ChannelID, err)
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Failed to look up game threads right now. Please try again.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}
	if !eligible {
		m.config.Config.Logger.Infof("game threads lookup skipped: intro unchanged since last run (thread=%s)", i.ChannelID)
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ The intro post has no changes to reflect in the list of game threads.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Update the message to show that the lookup is in progress
	// Button label is updated and the button is disabled
	autoMessage := AutoMessage{}
	autoMessage.guildId = i.GuildID
	autoMessage.feedChannelId = m.config.Config.GetIntroFeedChannelID()
	autoMessage.FindingState = true
	_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Flags:      discordgo.MessageFlagsIsComponentsV2,
			Components: autoMessage.Components(),
		},
	})

	// Call the agent to find the game threads for the user
	// If the agent reponds with an empty string,
	// 1. reset the auto message to the original state, and
	// 2. inform the user of the failure ephemerally
	prompt := fmt.Sprintf("Find the game threads for the games <@%s> plays.", userID)
	jsonReply := m.config.Agent.HandleInternal(s, prompt)
	if strings.TrimSpace(jsonReply) == "" {
		m.config.Config.Logger.Warnf("game threads lookup failed: agent returned empty response (thread=%s user=%s)", i.ChannelID, userID)
		autoMessage.FindingState = false
		components := autoMessage.Components()
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{Components: &components})
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Failed to look up game threads right now. Please try again.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Parse the agent's JSON response into a structured result.
	// If failed to parse,
	// 1. reset the auto message to the original state, and
	// 2. inform the user of the failure ephemerally
	var agentResult GameThreadsAgentResult
	if err := json.Unmarshal([]byte(jsonReply), &agentResult); err != nil {
		m.config.Config.Logger.Warnf("game threads lookup failed: invalid agent response json (thread=%s user=%s err=%v)", i.ChannelID, userID, err)
		autoMessage.FindingState = false
		components := autoMessage.Components()
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{Components: &components})
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Failed to look up game threads right now. Please try again.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Finding game threads succeeded, update the auto message to show the results.
	autoMessage.FindingState = false
	autoMessage.GameThreads = agentResult.GameThreads
	finalComponents := autoMessage.Components()
	_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{Components: &finalComponents})
	m.config.Config.Logger.Infof("game threads lookup completed (thread=%s user=%s results=%d)", i.ChannelID, userID, len(agentResult.GameThreads))

	// Track the execution timestamp of the game threads lookup for this intro thread in the database
	if err := m.config.DB.UpsertGameThreadsLookupExecution(i.ChannelID); err != nil && m.config.Config != nil {
		m.config.Config.Logger.Warnf("failed to update game threads lookup execution tracker for intro thread %s: %v", i.ChannelID, err)
	}
}
