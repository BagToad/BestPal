package nineteeneightyfour

import "gamerpal/internal/config"

// ConfigSettings declares the per-guild settings owned by the 1984 module,
// auto-collected into the config panel registry.
func (m *Module) ConfigSettings() []config.Setting {
	return []config.Setting{
		{
			Key:         config.Key1984LogChannelID,
			Category:    config.CategoryMisc,
			Label:       "1984 log channel",
			Description: "Channel where the 1984 module posts message activity logs.",
			Kind:        config.KindChannel,
		},
	}
}
