package commands

import (
	"fmt"
	"sort"

	"gamerpal/internal/config"
)

// CollectConfigSettings returns a Registry built from the union of settings
// declared by every module that implements config.ConfigProvider, plus a
// built-in core provider (shared server channels) and any extra providers
// passed in (e.g. the agent, which is not a command module).
//
// Mirrors CollectAgentTools: modules are visited in alphabetical order for
// stable ordering, and duplicate keys are skipped with a warning so two
// providers cannot silently fight over the same key.
func (h *ModuleHandler) CollectConfigSettings(extra ...config.ConfigProvider) *config.Registry {
	// Core provider first so shared server channels lead their categories,
	// then modules alphabetically, then extras (agent).
	providers := []namedProvider{{name: "core", provider: coreConfigProvider{}}}

	names := make([]string, 0, len(h.modules))
	for name := range h.modules {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if cp, ok := h.modules[name].(config.ConfigProvider); ok {
			providers = append(providers, namedProvider{name: name, provider: cp})
		}
	}
	for i, cp := range extra {
		if cp == nil {
			continue
		}
		providers = append(providers, namedProvider{name: fmt.Sprintf("extra#%d", i), provider: cp})
	}

	var out []config.Setting
	seen := make(map[string]string)
	for _, np := range providers {
		for _, s := range np.provider.ConfigSettings() {
			if prev, exists := seen[s.Key]; exists {
				h.config.Logger.Warnf(
					"config settings: provider %q declares duplicate key %q (already provided by %q); skipping",
					np.name, s.Key, prev,
				)
				continue
			}
			seen[s.Key] = np.name
			out = append(out, s)
		}
	}
	return config.NewRegistry(out)
}

type namedProvider struct {
	name     string
	provider config.ConfigProvider
}

// coreConfigProvider owns the server-wide channels and categories that several
// modules consume, so they have a single declared home rather than being
// scattered across feature modules.
type coreConfigProvider struct{}

func (coreConfigProvider) ConfigSettings() []config.Setting {
	return []config.Setting{
		{
			Key:         config.KeyModActionLogChannelID,
			Category:    config.CategoryChannels,
			Label:       "Mod action log",
			Description: "Channel where moderation actions (bans, scamguard) are logged.",
			Kind:        config.KindChannel,
		},
		{
			Key:         config.KeyLogChannelID,
			Category:    config.CategoryChannels,
			Label:       "General log",
			Description: "Channel for general bot logging.",
			Kind:        config.KindChannel,
		},
		{
			Key:         config.KeyVoiceSyncCategoryID,
			Category:    config.CategoryChannels,
			Label:       "Voice sync category",
			Description: "Category whose voice channels get permission-synced text chats.",
			Kind:        config.KindCategory,
		},
		{
			Key:         config.KeyHelpDeskChannelID,
			Category:    config.CategoryMisc,
			Label:       "Help desk channel",
			Description: "Channel used by the help desk / say workflows.",
			Kind:        config.KindChannel,
		},
		{
			Key:         config.KeyEventFeedChannelID,
			Category:    config.CategoryMisc,
			Label:       "Event feed channel",
			Description: "Channel where newly scheduled server events are announced.",
			Kind:        config.KindChannel,
		},
	}
}
