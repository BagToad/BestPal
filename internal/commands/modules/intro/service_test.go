package intro

import (
	"testing"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
)

func newFeedService(kv map[string]any) *IntroFeedService {
	return &IntroFeedService{deps: &types.Dependencies{Config: config.NewMockConfig(kv)}}
}

func TestMemberIsBooster(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name   string
		member *discordgo.Member
		want   bool
	}{
		{name: "nil member", member: nil, want: false},
		{name: "premium_since set (api truth)", member: &discordgo.Member{PremiumSince: &now}, want: true},
		{name: "no premium_since", member: &discordgo.Member{Roles: []string{"x"}}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFeedService(map[string]any{})
			assert.Equal(t, tc.want, svc.memberIsBooster(tc.member))
		})
	}
}

func TestCooldownHoursForMember(t *testing.T) {
	now := time.Now()

	t.Run("booster limit unset -> standard window even for booster", func(t *testing.T) {
		svc := newFeedService(map[string]any{"intro_feed_rate_limit_hours": 48})
		assert.Equal(t, 48, svc.cooldownHoursForMember(&discordgo.Member{PremiumSince: &now}))
	})

	t.Run("booster limit set -> booster window for booster", func(t *testing.T) {
		svc := newFeedService(map[string]any{
			"intro_feed_rate_limit_hours":         48,
			"intro_feed_booster_rate_limit_hours": 12,
		})
		assert.Equal(t, 12, svc.cooldownHoursForMember(&discordgo.Member{PremiumSince: &now}))
	})

	t.Run("booster limit set -> standard window for non-booster", func(t *testing.T) {
		svc := newFeedService(map[string]any{
			"intro_feed_rate_limit_hours":         48,
			"intro_feed_booster_rate_limit_hours": 12,
		})
		assert.Equal(t, 48, svc.cooldownHoursForMember(&discordgo.Member{Roles: []string{"x"}}))
	})

	t.Run("standard default (48) applies when standard limit unset", func(t *testing.T) {
		svc := newFeedService(map[string]any{"intro_feed_booster_rate_limit_hours": 6})
		assert.Equal(t, 48, svc.cooldownHoursForMember(&discordgo.Member{}))
	})
}

func TestPostAutoMessageToThread(t *testing.T) {
	t.Run("returns error when session is nil", func(t *testing.T) {
		svc := newFeedService(map[string]any{})
		err := svc.PostAutoMessageToThread("thread123", AutoPost{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "discord session not available")
	})
}

func TestPreambleBuilder(t *testing.T) {
	t.Run("builds feed-forwarded preamble with link", func(t *testing.T) {
		got := preambleBuilder(FeedForwardedState, "guild123", "feed456", 0)
		assert.Equal(t, "💥 Your intro is up on [the feed](https://discord.com/channels/guild123/feed456)\n\n`/intro` - find yours or another's intro again\n`/bump-intro` - repost to the feed", got)
	})

	t.Run("builds cooldown-skip preamble with remaining time", func(t *testing.T) {
		got := preambleBuilder(CooldownSkipState, "", "", 2*time.Hour+30*time.Minute)
		assert.Contains(t, got, "because you're still on cooldown (2h 30m remaining)")
		assert.Contains(t, got, "`/intro` - find yours or another's intro again")
		assert.Contains(t, got, "`/bump-intro` - repost to the feed when ready")
	})

	t.Run("uses default fallback preamble", func(t *testing.T) {
		got := preambleBuilder(DefaultState, "", "", 0)
		assert.Equal(t, autoPostDefaultPreamble, got)
	})
}
