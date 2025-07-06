package utils

import (
	"github.com/bwmarrin/discordgo"
)

var standardEmbedFooter = &discordgo.MessageEmbedFooter{
	Text: "Run /help for more options",
}

// NewEmbed creates a new embed with the standard footer and neutral color
func NewEmbed() *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Color:  Colors.Ok(),
		Footer: standardEmbedFooter,
	}
}

// NewErrorEmbed creates a new error embed with the given title and description
func NewOKEmbed(title, description string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Color:       Colors.Ok(),
		Footer:      standardEmbedFooter,
	}
}

// NewErrorEmbed creates a new error embed with the given title and description
func NewErrorEmbed(title, description string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "❌ " + title,
		Description: description,
		Color:       Colors.Error(),
		Footer:      standardEmbedFooter,
	}
}
