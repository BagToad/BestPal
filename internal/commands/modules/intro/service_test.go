package intro

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/database"
	"gamerpal/internal/forumcache"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// cooldownTransport keeps reconciliation on the real Discord REST path without network calls.
type cooldownTransport func(*http.Request) (*http.Response, error)

func (f cooldownTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReconcileIntroCooldownRole(t *testing.T) {
	for _, tc := range []struct {
		name                                                             string
		historyAge                                                       time.Duration
		history, bump, hasRole, booster, admin, noCache, nilDB, closedDB bool
		wantMethod                                                       string
	}{
		{name: "old intro fresh successful bump retains role", history: true, bump: true, hasRole: true},
		{name: "old intro recent feed post adds role", history: true, historyAge: 2 * time.Hour, wantMethod: http.MethodPut},
		{name: "latest bump wins over expired feed post", history: true, bump: true, wantMethod: http.MethodPut},
		{name: "expired history removes role", history: true, historyAge: 96 * time.Hour, hasRole: true, wantMethod: http.MethodDelete},
		{name: "no history removes role", hasRole: true, wantMethod: http.MethodDelete},
		{name: "booster expired shorter window removes role", history: true, historyAge: 24 * time.Hour, booster: true, hasRole: true, wantMethod: http.MethodDelete},
		{name: "normal member retains same age history", history: true, historyAge: 24 * time.Hour, hasRole: true},
		{name: "booster recent bump retains role", history: true, bump: true, historyAge: 2 * time.Hour, booster: true, hasRole: true},
		{name: "admin with role is skipped", admin: true, hasRole: true},
		{name: "admin without role is skipped", admin: true, history: true},
		{name: "cache unavailable still reconciles", noCache: true, hasRole: true, wantMethod: http.MethodDelete},
		{name: "DB unavailable preserves existing role", nilDB: true, hasRole: true},
		{name: "DB unavailable does not add role", nilDB: true},
		{name: "DB lookup error preserves existing role", closedDB: true, hasRole: true},
		{name: "DB lookup error does not add role", closedDB: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			cfg, cache := forumcache.NewTestForumCache(map[string]any{
				"gamerpals_server_id": "guild", "gamerpals_introductions_forum_channel_id": "forum",
				"intro_cooldown_role_id": "cooldown", "intro_feed_rate_limit_hours": 48,
				"intro_feed_booster_rate_limit_hours": 12,
			})
			cache.RegisterForum("forum")
			// A real old snowflake makes creation-time eligibility disagree with fresh feed history.
			oldThreadID := fmt.Sprint((now.Add(-96*time.Hour).UnixMilli() - 1420070400000) << 22)
			cache.OnThreadCreate(nil, &discordgo.ThreadCreate{Channel: &discordgo.Channel{
				ID: oldThreadID, GuildID: "guild", ParentID: "forum", OwnerID: "user",
			}})
			dbPath := filepath.Join(t.TempDir(), "intro.db")
			db, err := database.NewDB(dbPath)
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			if tc.history {
				// Seed an older success too: the latest successful post/bump must win.
				require.NoError(t, db.RecordIntroFeedPost("user", oldThreadID, "older", false))
				require.NoError(t, db.RecordIntroFeedPost("user", oldThreadID, "latest", tc.bump))
				conn, err := sql.Open("sqlite3", dbPath)
				require.NoError(t, err)
				_, err = conn.Exec("UPDATE intro_feed_posts SET posted_at = ? WHERE feed_message_id = 'older'", now.Add(-120*time.Hour).UTC().Format("2006-01-02 15:04:05"))
				require.NoError(t, err)
				_, err = conn.Exec("UPDATE intro_feed_posts SET posted_at = ? WHERE feed_message_id = 'latest'", now.Add(-tc.historyAge).UTC().Format("2006-01-02 15:04:05"))
				require.NoError(t, err)
				require.NoError(t, conn.Close())
			}
			// Tracking-only records have no feed message and must not start a cooldown.
			require.NoError(t, db.RecordIntroFeedPost("user", oldThreadID, "", false))
			member := &discordgo.Member{GuildID: "guild", User: &discordgo.User{ID: "user"}}
			if tc.hasRole {
				member.Roles = []string{"cooldown"}
			}
			if tc.booster {
				member.PremiumSince = &now
			}
			bits := int64(0)
			if tc.admin {
				bits = discordgo.PermissionAdministrator
			}
			session, err := discordgo.New("")
			require.NoError(t, err)
			require.NoError(t, session.State.GuildAdd(&discordgo.Guild{
				ID: "guild", Roles: []*discordgo.Role{{ID: "guild", Permissions: bits}},
				Members: []*discordgo.Member{member},
			}))
			require.NoError(t, session.State.ChannelAdd(&discordgo.Channel{ID: "forum", GuildID: "guild"}))
			memberJSON, err := json.Marshal([]*discordgo.Member{member})
			require.NoError(t, err)
			var mutations []string
			memberPages := 0
			session.Client = &http.Client{Transport: cooldownTransport(func(r *http.Request) (*http.Response, error) {
				code, body := http.StatusOK, "[]"
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/guilds/guild/members"):
					memberPages++
					if r.URL.Query().Get("after") == "" {
						body = string(memberJSON)
					} else {
						assert.Equal(t, "user", r.URL.Query().Get("after"))
					}
				case (r.Method == http.MethodPut || r.Method == http.MethodDelete) && strings.HasSuffix(r.URL.Path, "/guilds/guild/members/user/roles/cooldown"):
					mutations = append(mutations, r.Method)
					code, body = http.StatusNoContent, ""
				default:
					t.Errorf("unexpected Discord HTTP: %s %s", r.Method, r.URL.Path)
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			if tc.closedDB {
				require.NoError(t, db.Close())
			}
			deps := &types.Dependencies{Config: cfg, Session: session, DB: db, ForumCache: cache}
			if tc.nilDB {
				deps.DB = nil
			}
			if tc.noCache {
				deps.ForumCache = nil
			}
			svc := NewIntroFeedService(deps)
			require.NoError(t, svc.ScheduledFuncs()["@hourly"]())
			if tc.wantMethod == "" {
				assert.Empty(t, mutations)
			} else {
				assert.Equal(t, []string{tc.wantMethod}, mutations)
			}
			if !tc.nilDB {
				assert.Equal(t, 2, memberPages, "exercise member pagination through the real reconciler")
			}
		})
	}
}
