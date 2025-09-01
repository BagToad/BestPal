package commands

import (
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"slices"

	"github.com/bwmarrin/discordgo"
)

func (h *SlashHandler) handleConfig(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !utils.IsSuperAdmin(i.User.ID, h.config) {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
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
				switch subOption.Name {
				case "key":
					key = subOption.StringValue()
				case "value":
					value = subOption.StringValue()
				}
			}
			handleConfigSet(s, i, h.config, key, value)
		case "list-keys":
			handleConfigListKeys(s, i, h.config)
		}
	}
}

func handleConfigSet(s *discordgo.Session, i *discordgo.InteractionCreate, cfg *config.Config, key, value string) {
	if key == "" || value == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
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
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
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
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "✅ Configuration updated successfully.",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func handleConfigListKeys(s *discordgo.Session, i *discordgo.InteractionCreate, cfg *config.Config) {
	// List of configuration keys with their current values
	// Note: sensitive keys like tokens are excluded from showing values

	type configItem struct {
		key       string
		showValue bool
	}

	configItems := []configItem{
		{"igdb_client_id", false},                          // Token-like, don't show value
		{"igdb_client_token", false},                       // Token, don't show value
		{"github_models_token", false},                     // Token, don't show value
		{"crypto_salt", false},                             // Sensitive, don't show value
		{"gamerpals_server_id", true},                      // Harmless ID
		{"gamerpals_mod_action_log_channel_id", true},      // Harmless ID
		{"gamerpals_pairing_category_id", true},            // Harmless ID
		{"gamerpals_introductions_forum_channel_id", true}, // Harmless ID
		{"new_pals_system_enabled", true},                  // Boolean setting
		{"new_pals_role_id", true},                         // Harmless ID
		{"new_pals_channel_id", true},                      // Harmless ID
		{"new_pals_keep_role_duration", true},              // Duration setting
		{"new_pals_time_between_welcome_messages", true},   // Duration setting
		{"database_path", true},                            // File path
		{"log_dir", true},                                  // Directory path
	}

	// Format the keys into a readable list
	var keysList string
	for _, item := range configItems {
		if item.showValue {
			value := cfg.GetString(item.key)
			if value == "" {
				value = "(not set)"
			}
			keysList += "• `" + item.key + "`: `" + value + "`\n"
		} else {
			keysList += "• `" + item.key + "`: *(hidden)*\n"
		}
	}

	content := "📋 **Available Configuration Keys:**\n\n" + keysList + "\n*Use `/config set <key> <value>` to modify any of these keys.*"

	// Respond to the interaction
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}
