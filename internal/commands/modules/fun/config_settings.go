package fun

import (
	"sort"

	"gamerpal/internal/config"
)

// ConfigSettings declares the per-guild settings owned by the fun module
// (the translate style), auto-collected into the config panel registry. The
// enum options are derived from translateLanguages so adding a new style here
// makes it selectable in the panel for free.
func (m *Module) ConfigSettings() []config.Setting {
	opts := []config.Option{{Value: "random", Label: "Random"}}

	keys := make([]string, 0, len(translateLanguages))
	for k := range translateLanguages {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		opts = append(opts, config.Option{Value: k, Label: translateLanguages[k].Name})
	}

	return []config.Setting{
		{
			Key:         config.KeyTranslateLanguage,
			Category:    config.CategoryMisc,
			Label:       "Translate style",
			Description: "Style used by the translate command. \"Random\" picks one per message.",
			Kind:        config.KindEnum,
			Default:     "random",
			EnumOptions: opts,
		},
	}
}
