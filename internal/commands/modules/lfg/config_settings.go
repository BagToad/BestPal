package lfg

import "gamerpal/internal/config"

// ConfigSettings declares the per-guild settings owned by the LFG module,
// auto-collected into the config panel registry.
func (m *Module) ConfigSettings() []config.Setting {
	return []config.Setting{
		{
			Key:         config.KeyLFGForumChannelID,
			Category:    config.CategoryLFG,
			Label:       "LFG forum",
			Description: "Forum channel for looking-for-game posts.",
			Kind:        config.KindChannel,
		},
		{
			Key:         config.KeyLFGNowPanelChannelID,
			Category:    config.CategoryLFG,
			Label:       "Looking NOW panel channel",
			Description: "Channel where the persistent Looking NOW panel lives.",
			Kind:        config.KindChannel,
		},
		{
			Key:         config.KeyLFGNowRoleID,
			Category:    config.CategoryLFG,
			Label:       "Looking NOW role",
			Description: "Role pinged/assigned for Looking NOW.",
			Kind:        config.KindRole,
		},
		{
			Key:         config.KeyLFGNowRoleDuration,
			Category:    config.CategoryLFG,
			Label:       "Looking NOW role duration",
			Description: "How long the Looking NOW role is held (e.g. 1h, 48h).",
			Kind:        config.KindDuration,
			Default:     "1h",
		},
	}
}
