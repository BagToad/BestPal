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
		err := svc.PostAutoMessageToThread("guild123", "thread123")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "discord session not available")
	})

	t.Run("returns error when feed channel not configured", func(t *testing.T) {
		svc := newFeedService(map[string]any{})
		svc.deps.Session = &discordgo.Session{}
		err := svc.PostAutoMessageToThread("guild123", "thread123")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "intro feed channel not configured")
	})

	t.Run("builds the expected message text", func(t *testing.T) {
		got := buildAutoIntroMessage("guild123", "feed456")
		assert.Equal(t, "💥 Your intro is up on [the feed](https://discord.com/channels/guild123/feed456)\n\n`/intro` - find yours or another's intro again\n`/bump-intro` - repost to the feed", got)
	})
}
