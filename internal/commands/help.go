package commands

import (
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// handleHelp handles the help slash command
func (h *SlashCommandHandler) handleHelp(s *discordgo.Session, i *discordgo.InteractionCreate) {
	embed := &discordgo.MessageEmbed{
		Title:       "🎮 Best Pal Bot - Help",
		Description: "A bot for r/GamerPals. Check out the code on [GitHub](https://github.com/BagToad/BestPal)",
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
				Name:   "/intro",
				Value:  "Look up a user's latest introduction post\n• Use `/intro` to find your own introduction\n• Use `/intro user:@username` to find someone else's introduction",
				Inline: false,
			},
			{
				Name:   "/time",
				Value:  "Time-related utilities\n• Use `/time datetime:2025-08-25 3:00 PM` to convert dates to Discord timestamps\n• Use `full:true` to see all Discord timestamp formats",
				Inline: false,
			},
			{
				Name:   "/help",
				Value:  "Show this help message",
				Inline: false,
			},
			{
				Name:   "/roulette help",
				Value:  "Show detailed help for roulette pairing commands",
				Inline: false,
			},
			{
				Name:   "🛠️ Moderator Commands:",
				Inline: false,
			},
			{
				Name:   "/userstats",
				Value:  "Show member statistics for the server\n• Use `stats:overview` or `stats:daily` for different views",
				Inline: false,
			},
			{
				Name:   "/welcome",
				Value:  "Generate a welcome message for new members\n• Use `/welcome minutes:30` to preview\n• Use `execute:true` to post the message directly",
				Inline: false,
			},
			{
				Name:   "🚀 Admin Commands:",
				Inline: false,
			},
			{
				Name:   "/say",
				Value:  "Send an anonymous message to a specified channel\n• Use `/say channel:#general message:Hello everyone!` to send a message",
				Inline: false,
			},
			{
				Name:   "/prune-inactive",
				Value:  "Remove users without any roles (dry run by default)\n• Use `execute:true` to actually remove users",
				Inline: false,
			},
			{
				Name:   "/roulette-admin help",
				Value:  "Show detailed help for roulette admin commands",
				Inline: false,
			},
		},
	}

	// Respond immediately with the embed
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	})
}
