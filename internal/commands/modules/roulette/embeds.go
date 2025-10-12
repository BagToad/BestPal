package roulette

import (
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// rouletteHelpEmbed creates the help embed for roulette commands
func rouletteHelpEmbed() *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🎲 Roulette Pairing System - Help",
		Description: "Find some GamerPals! Meet new people!",
		Color:       utils.Colors.Info(),
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "📝 How It Works:",
				Value:  "1. Sign up for pairing (e.g. `/roulette signup`)\n2. Add games you want to play (e.g. `/roulette games-add name:Overwatch 2`)\n3. Wait for scheduled pairing events\n4. Get matched with other players who share your games",
				Inline: false,
			},
			{
				Name:   "🎮 Available Commands:",
				Inline: false,
			},
			{
				Name:   "/roulette signup",
				Value:  "Sign up for the next pairing event\n• You'll be matched with other players who share games with you",
				Inline: false,
			},
			{
				Name:   "/roulette nah",
				Value:  "Remove yourself from pairing\n• Use this if you no longer want to be paired",
				Inline: false,
			},
			{
				Name:   "/roulette games-add",
				Value:  "Add games to your pairing list\n• Example: `/roulette games-add name:Overwatch 2`\n• You can add multiple games: `Overwatch 2, Minecraft, Valorant`\n• Only games in your list will be considered for matching",
				Inline: false,
			},
			{
				Name:   "/roulette games-remove",
				Value:  "Remove games from your pairing list\n• Example: `/roulette games-remove name:Overwatch 2`\n• You can remove multiple games: `Overwatch 2, Minecraft`",
				Inline: false,
			},
			{
				Name:   "📅 Pairing Schedule:",
				Value:  "Pairing events are scheduled by server admins.",
				Inline: false,
			},
		},
	}
}

// rouletteAdminHelpEmbed creates the help embed for roulette admin commands
func rouletteAdminHelpEmbed() *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🚀 Roulette Admin Commands - Help",
		Description: "Administrative commands for managing the roulette pairing system",
		Color:       utils.Colors.Warning(),
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "🔧 Management Commands:",
				Inline: false,
			},
			{
				Name:   "/roulette-admin debug",
				Value:  "Show detailed system information\n• View current signups and their games\n• Check scheduled pairing times",
				Inline: false,
			},
			{
				Name:   "/roulette-admin pair",
				Value:  "Execute or schedule pairing events\n• `time:datetime` - Schedule for specific time\n• `immediate-pair:true` - Execute pairing now\n• `dryrun:false` - Actually create channels (default: true for testing)\n\nExample: `/roulette-admin pair time:2025-08-15 8:00 PM`",
				Inline: false,
			},
			{
				Name:   "/roulette-admin reset",
				Value:  "Delete all existing pairing channels\n• Removes all channels created by previous pairings",
				Inline: false,
			},
			{
				Name:   "/roulette-admin delete-schedule",
				Value:  "Cancel the currently scheduled pairing\n• Removes any scheduled pairing time\n• Does not affect current signups",
				Inline: false,
			},
			{
				Name:   "🧪 Testing Commands:",
				Inline: false,
			},
			{
				Name:   "/roulette-admin simulate-pairing",
				Value:  "Test the pairing system with fake users\n• `user-count:8` - Number of fake users (4-50)\n• `create-channels:true` - Actually create test channels\n• Useful for testing pairing algorithms",
				Inline: false,
			},
			{
				Name:   "📋 Best Practices:",
				Value:  "• Always use `dryrun:true` first to test pairing",
				Inline: false,
			},
		},
	}
}
