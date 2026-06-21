package intro

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// HandleComponent routes component interactions for the intro module
func (m *Module) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.MessageComponentData() == nil {
		return
	}

	cid := i.MessageComponentData().CustomID
	if strings.HasPrefix(cid, "intro:lookup-games") {
		m.handleLookupGamesComponent(s, i)
	}
}

// handleLookupGamesComponent handles the "Lookup Game Threads" button click
func (m *Module) handleLookupGamesComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract context from interaction metadata (not custom ID)
	threadID := i.ChannelID
	if threadID == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Could not determine thread ID",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Determine user ID: prefer Member context, fall back to User
	userID := ""
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}

	if userID == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Could not determine user ID",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Defer the response while we prepare to send to the agent
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: 0, // Not ephemeral; will post to the thread
		},
	})

	// Dispatch to agent
	m.handleLookupGameThreads(s, i, threadID, userID, i.GuildID)
}

// handleLookupGameThreads triggers the agent to find game threads for the intro user
func (m *Module) handleLookupGameThreads(s *discordgo.Session, i *discordgo.InteractionCreate, threadID, userID, guildID string) {
	if m.config == nil {
		return
	}

	// Log the button click
	userMention := "Member"
	if i.Member != nil && i.Member.User != nil {
		userMention = i.Member.User.Mention()
	}

	logMsg := fmt.Sprintf("%s clicked 'Lookup Game Threads' for intro thread by <@%s>", userMention, userID)
	if err := introLog(m.config, s, logMsg); err != nil {
		m.config.Config.Logger.Warnf("failed to log lookup game threads action: %v", err)
	}

	// TODO: In a real implementation, this would trigger the Copilot agent
	// with a message like: "Find the game threads for the games <@{userID}> plays and post the results to thread <#{threadID}>"
	// For now, we respond with a message
	threadMention := fmt.Sprintf("<#%s>", threadID)
	responseMsg := fmt.Sprintf("🎮 Looking up game threads for <@%s>...\n\nAgent would search for games in the intro post and post results to %s", userID, threadMention)

	_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{
		Content: &responseMsg,
	})
}
