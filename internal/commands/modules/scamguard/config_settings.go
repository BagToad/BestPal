package scamguard

import "gamerpal/internal/config"

// scamGuardActionOptions are the valid enforcement actions, surfaced as an enum
// in the config panel.
var scamGuardActionOptions = []config.Option{
	{Value: "log", Label: "Log only"},
	{Value: "delete", Label: "Delete message"},
	{Value: "timeout", Label: "Delete + timeout"},
}

// ConfigSettings declares the per-guild settings owned by the scamguard module,
// auto-collected into the config panel registry.
func (m *Module) ConfigSettings() []config.Setting {
	return []config.Setting{
		{
			Key:         config.KeyScamGuardEnabled,
			Category:    config.CategoryScamGuard,
			Label:       "ScamGuard enabled",
			Description: "Master switch for perceptual-hash scam image detection.",
			Kind:        config.KindBool,
			Default:     false,
		},
		{
			Key:         config.KeyScamGuardAction,
			Category:    config.CategoryScamGuard,
			Label:       "Action on match",
			Description: "What happens when an image matches a known-bad hash.",
			Kind:        config.KindEnum,
			Default:     "timeout",
			EnumOptions: scamGuardActionOptions,
		},
		{
			Key:         config.KeyScamGuardHashThreshold,
			Category:    config.CategoryScamGuard,
			Label:       "Hash match threshold",
			Description: "Max Hamming distance for a match (0 = exact). Capped at 16.",
			Kind:        config.KindInt,
			Default:     8,
		},
		{
			Key:         config.KeyScamGuardTimeoutDuration,
			Category:    config.CategoryScamGuard,
			Label:       "Timeout duration",
			Description: "How long a matched author is timed out (e.g. 168h).",
			Kind:        config.KindDuration,
			Default:     "168h",
		},
		{
			Key:         config.KeyScamGuardLogChannelID,
			Category:    config.CategoryScamGuard,
			Label:       "ScamGuard log channel",
			Description: "Where scamguard actions are logged. Falls back to the mod action log.",
			Kind:        config.KindChannel,
		},
	}
}
