package commands

import (
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// handleHelp handles the help slash command
func (h *Handler) handleHelp(s *discordgo.Session, i *discordgo.InteractionCreate) {
	embed := &discordgo.MessageEmbed{
		Title:       "🎮 GamerPal Bot - Help",
		Description: "A bot for r/GamerPals. Check out the code on [GitHub](https://github.com/bagtoad/bestpal)",
		Color:       utils.Colors.Info(),
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "🤖 Available Commands:",
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
				Name:   "/help",
				Value:  "Show this help message",
				Inline: false,
			},
			{
				Name:   "🚀 Commands for Admins:",
				Inline: false,
			},
			{
				Name:   "/prune-inactive",
				Value:  "Remove users without any roles (dry run by default)\n• Use `execute:true` to actually remove users\n",
				Inline: false,
			},
			{
				Name:   "/userstats",
				Value:  "Shows the number of users in the server (excluding bots)",
				Inline: false,
			},
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
