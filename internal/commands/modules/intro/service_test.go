package intro

import (
	"testing"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/forumcache"

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

func TestCheckIntroRoleEligibility(t *testing.T) {
	now := time.Now()

	t.Run("no intro metadata means eligible", func(t *testing.T) {
		svc := newFeedService(map[string]any{"intro_feed_rate_limit_hours": 48})
		assert.True(t, svc.checkIntroRoleEligibility(&discordgo.Member{}, nil, now))
	})

	t.Run("unknown intro timestamp means eligible", func(t *testing.T) {
		svc := newFeedService(map[string]any{"intro_feed_rate_limit_hours": 48})
		assert.True(t, svc.checkIntroRoleEligibility(&discordgo.Member{}, &forumcache.ThreadMeta{}, now))
	})

	t.Run("recent intro inside cooldown means not eligible", func(t *testing.T) {
		svc := newFeedService(map[string]any{"intro_feed_rate_limit_hours": 48})
		meta := &forumcache.ThreadMeta{CreatedAt: now.Add(-2 * time.Hour)}
		assert.False(t, svc.checkIntroRoleEligibility(&discordgo.Member{}, meta, now))
	})

	t.Run("old intro outside cooldown means eligible", func(t *testing.T) {
		svc := newFeedService(map[string]any{"intro_feed_rate_limit_hours": 48})
		meta := &forumcache.ThreadMeta{CreatedAt: now.Add(-96 * time.Hour)}
		assert.True(t, svc.checkIntroRoleEligibility(&discordgo.Member{}, meta, now))
	})
}

func TestIsModeratorPermissions(t *testing.T) {
	assert.True(t, isModeratorPermissions(discordgo.PermissionManageMessages))
	assert.True(t, isModeratorPermissions(discordgo.PermissionAdministrator))
	assert.False(t, isModeratorPermissions(discordgo.PermissionSendMessages))
}
