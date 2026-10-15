package intro

import (
	"bytes"
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

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Feed sends and helper writes use Discord's real JSON codec, but never the network.
// SQLite triggers deliberately persist a different time from the process clock.
// This makes a fabricated time.Now() timestamp fail observably.
type cooldownPostFixture struct {
	svc                    *IntroFeedService
	conn                   *sql.DB
	member                 *discordgo.Member
	thread                 *discordgo.Channel
	helper                 *discordgo.Message
	logs                   bytes.Buffer
	feedPosts, helperPosts int
	failFeed               bool
	persistedAt            time.Time
}

func newCooldownPostFixture(t *testing.T) *cooldownPostFixture {
	t.Helper()
	cfg := config.NewMockConfig(map[string]any{
		"gamerpals_server_id": "guild", "gamerpals_introductions_forum_channel_id": "forum",
		"intro_feed_channel_id": "feed", "intro_cooldown_role_id": "cooldown",
		"intro_feed_rate_limit_hours": 48, "intro_feed_booster_rate_limit_hours": 12,
	})
	path := filepath.Join(t.TempDir(), "feed.db")
	db, err := database.NewDB(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	conn, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	session, err := discordgo.New("")
	require.NoError(t, err)
	session.MaxRestRetries = 0
	f := &cooldownPostFixture{
		conn:   conn,
		svc:    NewIntroFeedService(&types.Dependencies{Config: cfg, Session: session, DB: db}),
		member: &discordgo.Member{GuildID: "guild", User: &discordgo.User{ID: "user", Username: "User"}},
		// A years-old intro must not determine the new cooldown.
		thread:      &discordgo.Channel{ID: "1000000000000000000", GuildID: "guild", ParentID: "forum", OwnerID: "user", Name: "Intro"},
		persistedAt: time.Date(2026, 9, 7, 16, 23, 45, 0, time.UTC),
	}
	cfg.Logger.SetOutput(&f.logs)
	session.State.User = &discordgo.User{ID: "bot", Bot: true}
	require.NoError(t, session.State.GuildAdd(&discordgo.Guild{
		ID: "guild", Roles: []*discordgo.Role{{ID: "guild"}}, Members: []*discordgo.Member{f.member},
	}))
	require.NoError(t, session.State.ChannelAdd(&discordgo.Channel{ID: "forum", GuildID: "guild"}))
	session.Client = &http.Client{Transport: cooldownTransport(func(r *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, `{}`
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/guilds/guild/members/user"):
			data, err := json.Marshal(f.member)
			require.NoError(t, err)
			body = string(data)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/"+f.thread.ID):
			data, err := json.Marshal(f.thread)
			require.NoError(t, err)
			body = string(data)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/channels/feed/messages"):
			if f.failFeed {
				status, body = http.StatusForbidden, `{"message":"send denied","code":50013}`
				break
			}
			f.feedPosts++
			body = fmt.Sprintf(`{"id":"feed-%d"}`, f.feedPosts)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/channels/"+f.thread.ID+"/messages"):
			f.helperPosts++
			require.NoError(t, json.NewDecoder(r.Body).Decode(&f.helper))
			f.helper.ID = "helper"
			f.helper.ChannelID = f.thread.ID
			f.helper.Author = session.State.User
			data, err := json.Marshal(f.helper)
			require.NoError(t, err)
			body = string(data)
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/roles/cooldown"):
			status, body = http.StatusNoContent, ""
		default:
			t.Errorf("unexpected Discord HTTP: %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	f.setPersistedTime(t, f.persistedAt)
	return f
}

func (f *cooldownPostFixture) setPersistedTime(t *testing.T, postedAt time.Time) {
	t.Helper()
	f.persistedAt = postedAt
	_, err := f.conn.Exec("DROP TRIGGER IF EXISTS test_post_time")
	require.NoError(t, err)
	_, err = f.conn.Exec(fmt.Sprintf(`CREATE TRIGGER test_post_time AFTER INSERT ON intro_feed_posts BEGIN
 UPDATE intro_feed_posts SET posted_at = '%s' WHERE id = NEW.id; END`, postedAt.UTC().Format("2006-01-02 15:04:05")))
	require.NoError(t, err)
}

func TestInitialIntroExpectedCooldownReset(t *testing.T) {
	for _, tc := range []struct {
		name                                                                                           string
		booster, fallback, admin, nilDB, closedDB, recordFail, readFail, sendFail, noHistory, zeroTime bool
	}{
		{name: "no history after recording omits line", noHistory: true}, {name: "zero persisted time omits line", zeroTime: true},
		{name: "normal"}, {name: "booster", booster: true}, {name: "booster fallback", booster: true, fallback: true},
		{name: "admin omits line", admin: true}, {name: "no database omits line", nilDB: true},
		{name: "closed database omits line", closedDB: true}, {name: "record failure does not reuse old history", recordFail: true},
		{name: "read failure omits line", readFail: true}, {name: "failed feed has no helper or history", sendFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCooldownPostFixture(t)
			if tc.booster {
				f.member.PremiumSince = &f.persistedAt
			}
			if tc.fallback {
				f.svc.deps.Config.Set("intro_feed_booster_rate_limit_hours", 0)
			}
			if tc.admin {
				guild, err := f.svc.deps.Session.State.Guild("guild")
				require.NoError(t, err)
				guild.Roles[0].Permissions = discordgo.PermissionAdministrator
			}
			if tc.nilDB {
				f.svc.deps.DB = nil
			}
			if tc.closedDB {
				require.NoError(t, f.svc.deps.DB.Close())
			}
			if tc.recordFail {
				require.NoError(t, f.svc.deps.DB.RecordIntroFeedPost("user", "older-thread", "older-feed", false))
				_, err := f.conn.Exec(`CREATE TRIGGER fail_record BEFORE INSERT ON intro_feed_posts BEGIN SELECT RAISE(FAIL, 'record unavailable'); END`)
				require.NoError(t, err)
			}
			if tc.readFail {
				_, err := f.conn.Exec(`DROP TRIGGER test_post_time; CREATE TRIGGER fail_read AFTER INSERT ON intro_feed_posts BEGIN UPDATE intro_feed_posts SET posted_at = NULL WHERE id = NEW.id; END`)
				require.NoError(t, err)
				// A nullable database timestamp cannot be scanned into time.Time.
			}
			if tc.noHistory {
				_, err := f.conn.Exec(`CREATE TRIGGER clear_history AFTER INSERT ON intro_feed_posts BEGIN DELETE FROM intro_feed_posts; END`)
				require.NoError(t, err)
			}
			if tc.zeroTime {
				f.setPersistedTime(t, time.Time{})
			}

			f.failFeed = tc.sendFail
			f.svc.HandleNewIntroThread(f.thread)
			if tc.sendFail {
				assert.Zero(t, f.feedPosts)
				assert.Zero(t, f.helperPosts)
				posts, err := f.svc.deps.DB.GetRecentIntroFeedPosts(time.Time{})
				require.NoError(t, err)
				assert.Empty(t, posts)
				return
			}
			require.Equal(t, 1, f.feedPosts)
			require.Equal(t, 1, f.helperPosts)
			require.Len(t, f.helper.Components, 2)
			content := f.helper.Components[0].(*discordgo.TextDisplay).Content
			if tc.admin || tc.nilDB || tc.closedDB || tc.recordFail || tc.readFail || tc.noHistory || tc.zeroTime {
				assert.NotContains(t, content, expectedCooldownResetLabel)
			} else {
				latest, err := f.svc.deps.DB.GetLastIntroFeedPostTime("user")
				require.NoError(t, err)
				require.True(t, f.persistedAt.Equal(latest))
				hours := 48
				if tc.booster && !tc.fallback {
					hours = 12
				}
				want := expectedCooldownReset(latest, hours, introCooldownSchedule)
				assert.Contains(t, content, fmt.Sprintf("Expected cooldown reset: <t:%d:R>", want.Unix()))
			}
		})
	}
}
