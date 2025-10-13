package lfg

import (
	"github.com/bwmarrin/discordgo"
)

// buildLFGModal creates the modal for finding/creating an LFG thread
func buildLFGModal() *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: lfgModalCustomID,
			Title:    "Find/Create LFG Thread",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						&discordgo.TextInput{
							CustomID:    lfgModalInputCustomID,
							Label:       "Game Name",
							Style:       discordgo.TextInputShort,
							Placeholder: "Enter game name",
							Required:    true,
							MaxLength:   100,
						},
					},
				},
			},
		},
	}
}
