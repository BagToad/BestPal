package feud

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gamerpal/internal/config"
)

// ConfigSettings declares the GP Feud settings.
func (m *Module) ConfigSettings() []config.Setting {
	c := config.CategoryFeud
	return []config.Setting{
		{Key: config.KeyFeudEnabled, Category: c, Label: "GP Feud enabled",
			Description: "Lets people answer and submit questions and play.", Kind: config.KindBool, Default: false},
		{Key: config.KeyFeudChannelID, Category: c, Label: "Game channel",
			Description: "Channel with the game board. The team threads are made here, so the bot needs Create Private Threads and Manage Threads.",
			Kind:        config.KindChannel, TextChannelOnly: true},
		{Key: config.KeyFeudReviewChannelID, Category: c, Label: "Review channel",
			Description: "Mod channel where submitted questions are posted to approve or reject. Without one, use /feud-admin review.",
			Kind:        config.KindChannel, TextChannelOnly: true},
		{Key: config.KeyFeudMinRespondents, Category: c, Label: "Answers needed",
			Description: "How many people must answer a question before it can be played.",
			Kind:        config.KindInt, Default: 10,
			Validate: intRange(config.FeudMinMinRespondents, config.FeudMaxMinRespondents)},
		{Key: config.KeyFeudTurnTimer, Category: c, Section: "Gameplay", Label: "Turn timer",
			Description: "How long a team has to settle on a guess (e.g. 90s).",
			Kind:        config.KindDuration, Default: "90s",
			Validate: durationRange(config.FeudMinTurnTimer, config.FeudMaxTurnTimer)},
		{Key: config.KeyFeudRounds, Category: c, Section: "Gameplay", Label: "Rounds per game",
			Description: "Each round is a new question.", Kind: config.KindInt, Default: 3,
			Validate: intRange(config.FeudMinRounds, config.FeudMaxRounds)},
		{Key: config.KeyFeudQuestionCooldown, Category: c, Section: "Gameplay", Label: "Question cooldown",
			Description: "How many games a played question sits out. If too few questions are ready, the longest-rested ones are used.",
			Kind:        config.KindInt, Default: 50, Validate: intRange(0, config.FeudMaxQuestionCooldown)},
	}
}

func intRange(lo, hi int) func(string) error {
	return func(v string) error {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("must be a whole number")
		}
		if n < lo || n > hi {
			return fmt.Errorf("must be between %d and %d", lo, hi)
		}
		return nil
	}
}

func durationRange(lo, hi time.Duration) func(string) error {
	return func(v string) error {
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("must be a duration like 90s or 2m")
		}
		if d < lo || d > hi {
			return fmt.Errorf("must be between %s and %s", lo, hi)
		}
		return nil
	}
}
