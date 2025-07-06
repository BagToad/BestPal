package commands

import (
	"github.com/bwmarrin/discordgo"
)

// handleHelp handles the help slash command
func (h *Handler) handleHelp(s *discordgo.Session, i *discordgo.InteractionCreate) {
	embed := &discordgo.MessageEmbed{
		Title:       "🎮 GamerPal Bot - Help",
		Description: "Here are the available slash commands:",
		Color:       0x0099ff,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "/userstats",
				Value:  "Shows the number of users in the server (excluding bots)",
				Inline: false,
			},
			{
				Name:   "/ping",
				Value:  "Check if the bot is responsive",
				Inline: false,
			},
			{
				Name:   "/game",
				Value:  "Look up information about a video game from IGDB\n• Use `/game name:GameName` to search for a game",
				Inline: false,
			},
			{
				Name:   "/time",
				Value:  "Time-related utilities\n• Use `/time parse datetime:2024-12-25 3:00 PM` to convert dates to Discord timestamps",
				Inline: false,
			},
			{
				Name:   "/prune-inactive",
				Value:  "Remove users without any roles (dry run by default)\n• Use `execute:true` to actually remove users\n• **Requires Administrator permissions**",
				Inline: false,
			},
			{
				Name:   "/help",
				Value:  "Show this help message",
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "GamerPal Bot",
		},
	}

	// Respond immediately with the embed
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	})
}
