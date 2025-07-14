package commands

import (
	"fmt"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// handleSay handles the say slash command
func (h *Handler) handleSay(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Parse command options
	options := i.ApplicationCommandData().Options
	if len(options) < 2 {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Missing required parameters. Please specify both channel and message.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Get channel and message from options
	var targetChannelID string
	var messageContent string

	for _, option := range options {
		switch option.Name {
		case "channel":
			targetChannelID = option.ChannelValue(s).ID
		case "message":
			messageContent = option.StringValue()
		}
	}

	// Validate inputs
	if targetChannelID == "" {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Invalid channel specified.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if messageContent == "" {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Message content cannot be empty.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Get the target channel to verify it exists and get its name
	targetChannel, err := s.Channel(targetChannelID)
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Unable to access the specified channel. Make sure the bot has permission to view and send messages in that channel.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Check if the bot has permission to send messages in the target channel
	permissions, err := s.UserChannelPermissions(s.State.User.ID, targetChannelID)
	if err != nil || permissions&discordgo.PermissionSendMessages == 0 {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("❌ I don't have permission to send messages in %s.", targetChannel.Mention()),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Add the anonymous admin tag to the message content to distinguish
	// different admins anonymously.
	anonID, err := utils.ObfuscateID(i.Member.User.ID, h.Config.GetCryptoSalt())
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Failed to obfuscate your ID. Please try again later.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	messageContent = fmt.Sprintf("%s\n\n**Sent by mod (%s)**", messageContent, anonID)

	// Send the message to the target channel
	sentMessage, err := s.ChannelMessageSend(targetChannelID, messageContent)
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("❌ Failed to send message to %s: %v", targetChannel.Mention(), err),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Create confirmation embed for the admin
	embed := &discordgo.MessageEmbed{
		Title:       "✅ Message Sent Successfully",
		Description: fmt.Sprintf("Your anonymous message has been sent to %s", targetChannel.Mention()),
		Color:       utils.Colors.Ok(),
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Channel",
				Value:  targetChannel.Mention(),
				Inline: true,
			},
			{
				Name:   "Message ID",
				Value:  sentMessage.ID,
				Inline: true,
			},
			{
				Name:   "Message Content",
				Value:  fmt.Sprintf("```%s```", messageContent),
				Inline: false,
			},
		},
	}

	// Respond to the admin with confirmation (ephemeral)
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}
