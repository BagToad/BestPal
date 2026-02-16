package config

import (
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"slices"

	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface for the config command
type ConfigModule struct {
	config *config.Config
}

// New creates a new config module
func New(deps *types.Dependencies) *ConfigModule {
	return &ConfigModule{}
}

// Register adds the config command to the command map
func (m *ConfigModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	m.config = deps.Config

	var adminPerms int64 = discordgo.PermissionAdministrator

	cmds["config"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "config",
			Description:              "Bot configuration commands (SuperAdmin only)",
			DefaultMemberPermissions: &adminPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextBotDM, discordgo.InteractionContextPrivateChannel},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "set",
					Description: "Set a configuration value",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "key",
							Description: "Configuration key",
							Required:    true,
						},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "value",
							Description: "Configuration value",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "list-keys",
					Description: "List all available configuration keys",
				},
			},
		},
		HandlerFunc: m.handleConfig,
	}
}

func (m *ConfigModule) handleConfig(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !utils.IsSuperAdmin(i.User.ID, m.config) {
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
			m.handleConfigSet(s, i, key, value)
		case "list-keys":
			m.handleConfigListKeys(s, i)
		}
	}
}

func (m *ConfigModule) handleConfigSet(s *discordgo.Session, i *discordgo.InteractionCreate, key, value string) {
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
	m.config.Set(key, value)

	// Respond to the interaction
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "✅ Configuration updated successfully.",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func (m *ConfigModule) handleConfigListKeys(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// List of configuration keys with their current values
	// Note: sensitive keys like tokens are excluded from showing values

	type configItem struct {
		key       string
		showValue bool
	}

	configItems := []configItem{
		{"bot_token", false},                               // Token, don't show value
		{"igdb_client_id", false},                          // Token-like, don't show value
		{"igdb_client_secret", false},                      // Token-like, don't show value
		{"igdb_client_token", false},                       // Token, don't show value
		{"github_models_token", false},                     // Token, don't show value
		{"crypto_salt", false},                             // Sensitive, don't show value
		{"super_admins", false},                            // Sensitive, don't show value
		{"gamerpals_server_id", true},                      // Harmless ID
		{"gamerpals_mod_action_log_channel_id", true},      // Harmless ID
		{"gamerpals_log_channel_id", true},                 // Harmless ID
		{"gamerpals_pairing_category_id", true},            // Harmless ID
		{"gamerpals_introductions_forum_channel_id", true}, // Harmless ID
		{"gamerpals_help_desk_channel_id", true},           // Harmless ID
		{"gamerpals_lfg_forum_channel_id", true},           // Harmless ID
		{"gamerpals_lfg_now_panel_channel_id", true},       // Harmless ID
		{"lfg_now_role_id", true},                          // Harmless ID
		{"lfg_now_role_duration", true},                    // Duration setting
		{"gamerpals_voice_sync_category_id", true},         // Harmless ID
		{"intro_feed_channel_id", true},                    // Harmless ID
		{"intro_feed_rate_limit_hours", true},              // Duration setting
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
			value := m.config.GetString(item.key)
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

// Service returns nil as this module has no services requiring initialization
func (m *ConfigModule) Service() types.ModuleService {
	return nil
}
