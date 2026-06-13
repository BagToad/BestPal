package intro

import "gamerpal/internal/config"

// ConfigSettings declares the per-guild settings owned by the intro module,
// auto-collected into the config panel registry.
func (m *IntroModule) ConfigSettings() []config.Setting {
	return []config.Setting{
		{
			Key:         config.KeyIntroductionsForumChannelID,
			Category:    config.CategoryIntro,
			Label:       "Introductions forum",
			Description: "Forum channel where members post introductions.",
			Kind:        config.KindChannel,
		},
		{
			Key:         config.KeyIntroFeedChannelID,
			Category:    config.CategoryIntro,
			Label:       "Intro feed channel",
			Description: "Channel where new introductions are forwarded.",
			Kind:        config.KindChannel,
		},
		{
			Key:         config.KeyIntroFeedRateLimitHours,
			Category:    config.CategoryIntro,
			Label:       "Feed rate limit (hours)",
			Description: "Hours between allowed feed posts per user. 0 uses the 48h default.",
			Kind:        config.KindInt,
			Default:     48,
		},
		{
			Key:         config.KeyIntroFeedBoosterRateLimit,
			Category:    config.CategoryIntro,
			Label:       "Booster feed rate limit (hours)",
			Description: "Hours between feed posts for boosters. 0 uses the standard rate limit.",
			Kind:        config.KindInt,
			Default:     0,
		},
	}
}
