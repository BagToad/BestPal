package commands

import (
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"slices"

	"github.com/bwmarrin/discordgo"
)

func (h *SlashHandler) handleConfig(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !utils.IsSuperAdmin(i.User.ID, h.config) {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ You do not have permission to use this command.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Get the options
	options := i.ApplicationCommandData().Options

	for _, option := range options {
		switch option.Name {
		case "set":
			var key string
			var value string
			for _, subOption := range option.Options {
				if subOption.Name == "key" {
					key = subOption.StringValue()
				} else if subOption.Name == "value" {
					value = subOption.StringValue()
				}
			}
			handleConfigSet(s, i, h.config, key, value)
		}
	}

	return
}

func handleConfigSet(s *discordgo.Session, i *discordgo.InteractionCreate, cfg *config.Config, key, value string) {
	if key == "" || value == "" {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Invalid key or value provided.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	forbiddenKeys := []string{"super_admins"}
	if slices.Contains(forbiddenKeys, key) {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ You cannot modify this configuration key.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Set the configuration value
	cfg.Set(key, value)

	// Respond to the interaction
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "✅ Configuration updated successfully.",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}
