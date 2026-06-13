package welcome

import "gamerpal/internal/config"

// ConfigSettings declares the per-guild settings owned by the welcome / New
// Pals module, auto-collected into the config panel registry.
func (m *WelcomeModule) ConfigSettings() []config.Setting {
	return []config.Setting{
		{
			Key:         config.KeyNewPalsSystemEnabled,
			Category:    config.CategoryNewPals,
			Label:       "New Pals system",
			Description: "Master switch for the New Pals welcome system.",
			Kind:        config.KindBool,
			Default:     false,
		},
		{
			Key:         config.KeyNewPalsRoleID,
			Category:    config.CategoryNewPals,
			Label:       "New Pals role",
			Description: "Role granted to newly joined members.",
			Kind:        config.KindRole,
		},
		{
			Key:         config.KeyNewPalsChannelID,
			Category:    config.CategoryNewPals,
			Label:       "New Pals channel",
			Description: "Channel where welcome messages are posted.",
			Kind:        config.KindChannel,
		},
		{
			Key:         config.KeyNewPalsKeepRoleDuration,
			Category:    config.CategoryNewPals,
			Label:       "Keep role duration",
			Description: "How long the New Pals role is kept (e.g. 168h).",
			Kind:        config.KindDuration,
		},
		{
			Key:         config.KeyNewPalsTimeBetweenMsgs,
			Category:    config.CategoryNewPals,
			Label:       "Time between welcome messages",
			Description: "Minimum spacing between welcome messages (e.g. 5m).",
			Kind:        config.KindDuration,
		},
	}
}
