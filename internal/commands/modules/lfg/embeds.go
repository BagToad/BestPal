package lfg

import (
	"fmt"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// foundThreadsEmbed creates an embed showing found LFG threads
func foundThreadsEmbed(fields []*discordgo.MessageEmbedField) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:  "Found LFG thread(s)",
		Color:  utils.Colors.Fancy(),
		Fields: fields,
	}
}

// createThreadSuggestionsEmbed creates an embed with game suggestions for thread creation
func createThreadSuggestionsEmbed(suggestionsText string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:  "Create a thread suggestions",
		Color:  utils.Colors.Fancy(),
		Fields: []*discordgo.MessageEmbedField{{Name: "Suggestions", Value: suggestionsText}},
	}
}

// threadCreatedEmbed creates an embed showing a created or found thread
func threadCreatedEmbed(ch *discordgo.Channel, created bool) *discordgo.MessageEmbed {
	status := "existing thread"
	if created {
		status = "created thread"
	}
	return &discordgo.MessageEmbed{
		Title: "Thread Created",
		Color: utils.Colors.Fancy(),
		Fields: []*discordgo.MessageEmbedField{
			{Name: fmt.Sprintf("%s (%s)", ch.Name, status), Value: fmt.Sprintf("- %s", threadLink(ch))},
		},
	}
}
