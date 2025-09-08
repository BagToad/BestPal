package commands

import (
	"github.com/bwmarrin/discordgo"
)

// handlePing handles the ping slash command
func (h *SlashCommandHandler) handlePing(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "🏓 Pong! Bot is online and responsive.",
		},
	})
}
