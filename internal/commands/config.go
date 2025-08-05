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
		case "list-keys":
			handleConfigListKeys(s, i)
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

	forbiddenKeys := []string{"super_admins", "bot_token"}
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

func handleConfigListKeys(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// List of all available configuration keys based on the config accessors
	// Note: super_admins and bot_token are excluded as they shouldn't be modified via Discord commands
	configKeys := []string{
		"igdb_client_id",
		"igdb_client_token",
		"crypto_salt",
		"github_models_token",
		"gamerpals_server_id",
		"gamerpals_mod_action_log_channel_id",
		"gamerpals_pairing_category_id",
		"gamerpals_introductions_forum_channel_id",
		"new_pals_system_enabled",
		"new_pals_role_id",
		"new_pals_channel_id",
		"new_pals_keep_role_duration",
		"new_pals_time_between_welcome_messages",
		"database_path",
		"log_dir",
	}

	// Format the keys into a readable list
	var keysList string
	for _, key := range configKeys {
		keysList += "• `" + key + "`\n"
	}

	content := "📋 **Available Configuration Keys:**\n\n" + keysList + "\n*Use `/config set <key> <value>` to modify any of these keys.*"

	// Respond to the interaction
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}
