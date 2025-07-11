package commands

import (
	"fmt"
	"gamerpal/internal/utils"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// handleWelcome handles the welcome slash command
func (h *Handler) handleWelcome(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Get the minutes parameter
	minutesOption := i.ApplicationCommandData().Options[0]
	minutes := int(minutesOption.IntValue())

	// Acknowledge the interaction immediately with ephemeral response
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	// Get guild members
	members, err := utils.GetAllGuildMembers(s, i.GuildID)
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ Error fetching server members: " + err.Error()),
		})
		return
	}

	// Find members who joined within the specified minutes
	cutoffTime := time.Now().Add(-time.Duration(minutes) * time.Minute)
	var newMembers []*discordgo.Member

	for _, member := range members {
		if member.User.Bot {
			continue // Skip bots
		}

		if member.JoinedAt.After(cutoffTime) {
			newMembers = append(newMembers, member)
		}
	}

	// Sort new members by join time (newest first)
	sort.Slice(newMembers, func(i, j int) bool {
		return newMembers[i].JoinedAt.After(newMembers[j].JoinedAt)
	})

	// Create the welcome message
	var welcomeMessage string
	if len(newMembers) == 0 {
		welcomeMessage = fmt.Sprintf("No new members found in the last %d minutes.", minutes)
	} else {
		// Create mentions for all new members
		var mentions []string
		for _, member := range newMembers {
			mentions = append(mentions, fmt.Sprintf("<@%s>", member.User.ID))
		}

		welcomeMessage = fmt.Sprintf("👋 Hia %s\n\nWelcome! Feel free introduce yourself in #intros or just hangout. We're glad you're here :)", strings.Join(mentions, " "))
	}

	// Create embed response
	embed := &discordgo.MessageEmbed{
		Title:       "🎉 Welcome Message Generator",
		Description: "Copy and paste the message below:",
		Color:       utils.Colors.Info(),
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Generated Welcome Message",
				Value:  welcomeMessage,
				Inline: false,
			},
			{
				Name:   "Details",
				Value:  fmt.Sprintf("Found %d new members in the last %d minutes", len(newMembers), minutes),
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "This message is only visible to you",
		},
	}

	// Send the ephemeral response
	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	})
}
