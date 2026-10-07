package intro

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/forumcache"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bumpHelperTransport struct {
	t                  *testing.T
	fixture            *cooldownPostFixture
	base               http.RoundTripper
	replies            []*discordgo.Message
	reads, edits       int
	failRead, failEdit bool
}

func stubBumpHelper(t *testing.T, f *cooldownPostFixture) *bumpHelperTransport {
	t.Helper()
	transport := &bumpHelperTransport{t: t, fixture: f, base: f.svc.deps.Session.Client.Transport}
	f.svc.deps.Session.Client.Transport = transport
	return transport
}

func (b *bumpHelperTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f, t := b.fixture, b.t
	status, body := http.StatusOK, `{}`
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/"+f.thread.ID+"/messages"):
		b.reads++
		assert.Equal(t, "100", r.URL.Query().Get("limit"))
		assert.Equal(t, f.thread.ID, r.URL.Query().Get("after"))
		assert.Empty(t, r.URL.Query().Get("before"))
		replies := b.replies
		if replies == nil {
			replies = []*discordgo.Message{f.helper}
		}
		// discordgo.Message intentionally omits Components when marshaling.
		// Supply the real REST field explicitly, as Discord does on reads.
		wire := make([]any, 0, len(replies))
		for _, message := range replies {
			if message == nil {
				wire = append(wire, nil)
				continue
			}
			wire = append(wire, struct {
				*discordgo.Message
				Components []discordgo.MessageComponent `json:"components"`
			}{message, message.Components})
		}
		data, err := json.Marshal(wire)
		require.NoError(t, err)
		body = string(data)
		if b.failRead {
			status, body = http.StatusForbidden, `{"message":"read denied","code":50013}`
		}
	case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/channels/"+f.thread.ID+"/messages/helper"):
		b.edits++
		var payload map[string]json.RawMessage
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		// Only components may be sent: no replacement content, embeds or attachments.
		require.Contains(t, payload, "components")
		for _, key := range []string{"content", "embeds", "attachments", "flags"} {
			assert.NotContains(t, payload, key)
		}
		if b.failEdit {
			status, body = http.StatusForbidden, `{"message":"edit denied","code":50013}`
			break
		}
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		var message discordgo.Message
		require.NoError(t, json.Unmarshal(data, &message))
		f.helper.Components = message.Components
		data, err = json.Marshal(f.helper)
		require.NoError(t, err)
		body = string(data)
	default:
		return b.base.RoundTrip(r)
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func legacyBumpHelper(t *testing.T, f *cooldownPostFixture, results, loading bool) {
	t.Helper()
	f.helper = decodedLookupMessage(t, results)
	f.helper.ID = "helper"
	f.helper.ChannelID = f.thread.ID
	f.helper.Author = f.svc.deps.Session.State.User
	setLookupButtonLoading(resolveLookupButton(f.helper.Components), loading)
	f.helper.Components[0].(*discordgo.TextDisplay).Content += "\nKeep this original helper text."
}

func TestBumpRefreshExpectedCooldownReset(t *testing.T) {
	for _, tc := range []struct {
		name                                                string
		initial, results, loading, booster, fallback, admin bool
	}{
		{name: "actual initial forward then repeated bumps", initial: true},
		{name: "legacy two components"}, {name: "legacy two loading", loading: true},
		{name: "legacy found game results", results: true}, {name: "legacy results while lookup loading", results: true, loading: true},
		{name: "booster window", results: true, booster: true}, {name: "booster fallback", booster: true, fallback: true},
		{name: "admin removes old mandatory cooldown", results: true, admin: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCooldownPostFixture(t)
			transport := stubBumpHelper(t, f)
			if tc.booster {
				f.member.PremiumSince = &f.persistedAt
			}
			if tc.fallback {
				f.svc.deps.Config.Set("intro_feed_booster_rate_limit_hours", 0)
			}
			if tc.initial {
				f.svc.HandleNewIntroThread(f.thread)
			} else {
				legacyBumpHelper(t, f, tc.results, tc.loading)
			}
			if tc.admin {
				f.helper.Components[0].(*discordgo.TextDisplay).Content = withExpectedCooldownReset(f.helper.Components[0].(*discordgo.TextDisplay).Content, f.persistedAt)
			}
			before := lookupSnapshot(t, f.helper.Components)
			originalPreamble := withExpectedCooldownReset(before.Components[0].(*discordgo.TextDisplay).Content, time.Time{})
			originalPosts := f.feedPosts
			for n := 1; n <= 3; n++ {
				f.setPersistedTime(t, f.persistedAt.Add(3*time.Hour))
				warning, err := f.svc.BumpIntroToFeed("guild", f.thread.ID, "user", "User", "Old intro", tc.admin)
				require.NoError(t, err)
				assert.Empty(t, warning, f.logs.String())
				assert.Equal(t, originalPosts+n, f.feedPosts)
				assert.Equal(t, before.Components[1:], f.helper.Components[1:], "keep result text and button IDs/style/loading")
				content := f.helper.Components[0].(*discordgo.TextDisplay).Content
				if tc.admin {
					assert.Equal(t, originalPreamble, content)
				} else {
					latest, err := f.svc.deps.DB.GetLastIntroFeedPostTime("user")
					require.NoError(t, err)
					require.True(t, f.persistedAt.Equal(latest))
					hours := 48
					if tc.booster && !tc.fallback {
						hours = 12
					}
					want := expectedCooldownReset(latest, hours, introCooldownSchedule)
					assert.Equal(t, originalPreamble+fmt.Sprintf("\nExpected cooldown reset: <t:%d:R>", want.Unix()), content)
					assert.Equal(t, 1, strings.Count(content, expectedCooldownResetLabel))
				}
			}
			assert.Equal(t, 3, transport.reads, "one bounded read per successful bump")
			if tc.admin {
				assert.Equal(t, 1, transport.edits)
			} else {
				assert.Equal(t, 3, transport.edits)
			}
			if tc.initial {
				assert.Equal(t, 1, f.helperPosts)
			} else {
				assert.Zero(t, f.helperPosts)
			}
		})
	}
}

func TestBumpRefreshUsesLatestSuccessfulUserHistory(t *testing.T) {
	f := newCooldownPostFixture(t)
	stubBumpHelper(t, f)
	legacyBumpHelper(t, f, true, false)
	// Retain the existing user-scoped policy, even across threads.
	latest := f.persistedAt.Add(10 * time.Hour)
	f.setPersistedTime(t, latest)
	require.NoError(t, f.svc.deps.DB.RecordIntroFeedPost("user", "another-thread", "newest", false))
	f.setPersistedTime(t, latest.Add(100*time.Hour))
	require.NoError(t, f.svc.deps.DB.RecordIntroFeedPost("user", f.thread.ID, "", false))
	f.setPersistedTime(t, latest.Add(-5*time.Hour))
	warning, err := f.svc.BumpIntroToFeed("guild", f.thread.ID, "user", "User", "Intro", false)
	require.NoError(t, err)
	assert.Empty(t, warning, f.logs.String())
	want := expectedCooldownReset(latest, 48, introCooldownSchedule)
	assert.Contains(t, f.helper.Components[0].(*discordgo.TextDisplay).Content, fmt.Sprintf("Expected cooldown reset: <t:%d:R>", want.Unix()))
}

func TestBumpRefreshFailuresPreserveSuccessfulPost(t *testing.T) {
	for _, tc := range []string{"feed", "record", "read history", "no database", "helper missing", "helper read", "helper edit", "bot identity", "ambiguous helper"} {
		t.Run(tc, func(t *testing.T) {
			f := newCooldownPostFixture(t)
			transport := stubBumpHelper(t, f)
			f.svc.HandleNewIntroThread(f.thread)
			before := lookupSnapshot(t, f.helper.Components)
			latestBefore, err := f.svc.deps.DB.GetLastIntroFeedPostTime("user")
			require.NoError(t, err)
			f.setPersistedTime(t, f.persistedAt.Add(3*time.Hour))
			switch tc {
			case "feed":
				f.failFeed = true
			case "record":
				_, err = f.conn.Exec(`CREATE TRIGGER fail_record BEFORE INSERT ON intro_feed_posts BEGIN SELECT RAISE(FAIL, 'record unavailable'); END`)
				require.NoError(t, err)
			case "read history":
				_, err = f.conn.Exec(`DROP TRIGGER test_post_time; CREATE TRIGGER fail_read AFTER INSERT ON intro_feed_posts BEGIN UPDATE intro_feed_posts SET posted_at = NULL; END`)
				require.NoError(t, err)
			case "no database":
				f.svc.deps.DB = nil
			case "helper missing":
				transport.replies = []*discordgo.Message{}
			case "helper read":
				transport.failRead = true
			case "helper edit":
				transport.failEdit = true
			case "bot identity":
				f.svc.deps.Session.State.User = nil
			case "ambiguous helper":
				transport.replies = []*discordgo.Message{f.helper, f.helper}
			}
			warning, err := f.svc.BumpIntroToFeed("guild", f.thread.ID, "user", "User", "Intro", false)
			if tc == "feed" {
				require.Error(t, err)
				assert.Empty(t, warning, f.logs.String())
				assert.Equal(t, 1, f.feedPosts)
				assert.Zero(t, transport.reads)
			} else {
				require.NoError(t, err)
				assert.Contains(t, warning, "feed post succeeded")
				assert.Equal(t, 2, f.feedPosts)
				assert.Contains(t, f.logs.String(), "Feed bump succeeded but helper refresh failed")
			}
			assert.Equal(t, 1, f.helperPosts, "never recreate a missing helper")
			assert.Equal(t, before.Components, f.helper.Components, "failed updates leave original timestamp and results intact")
			if tc == "feed" || tc == "record" {
				latest, err := f.svc.deps.DB.GetLastIntroFeedPostTime("user")
				require.NoError(t, err)
				assert.True(t, latestBefore.Equal(latest))
			} else if tc != "read history" && tc != "no database" {
				latest, err := f.svc.deps.DB.GetLastIntroFeedPostTime("user")
				require.NoError(t, err)
				assert.True(t, f.persistedAt.Equal(latest))
			}
		})
	}
}

func TestFindAutoIntroCommentRejectsUnrelatedMessages(t *testing.T) {
	for _, tc := range []string{"human author", "other bot", "webhook", "wrong channel", "no preamble", "wrong button", "extra child", "bad result", "wrong layout", "no author", "no message id"} {
		t.Run(tc, func(t *testing.T) {
			f := newCooldownPostFixture(t)
			transport := stubBumpHelper(t, f)
			legacyBumpHelper(t, f, true, false)
			switch tc {
			case "human author":
				f.helper.Author = &discordgo.User{ID: "user"}
			case "other bot":
				f.helper.Author = &discordgo.User{ID: "other-bot", Bot: true}
			case "webhook":
				f.helper.WebhookID = "webhook"
			case "wrong channel":
				f.helper.ChannelID = "other-thread"
			case "no preamble":
				f.helper.Components[0] = &discordgo.TextDisplay{Content: "Arbitrary bot comment"}
			case "wrong button":
				resolveLookupButton(f.helper.Components).CustomID = "other:button"
			case "extra child":
				row := f.helper.Components[1].(*discordgo.ActionsRow)
				row.Components = append(row.Components, &discordgo.Button{CustomID: "other"})
			case "bad result":
				f.helper.Components[2] = &discordgo.ActionsRow{}
			case "wrong layout":
				f.helper.Components = f.helper.Components[:1]
			case "no author":
				f.helper.Author = nil
			case "no message id":
				f.helper.ID = ""
			}
			before := lookupSnapshot(t, f.helper.Components)
			warning, err := f.svc.BumpIntroToFeed("guild", f.thread.ID, "user", "User", "Intro", false)
			require.NoError(t, err)
			assert.NotEmpty(t, warning)
			assert.Zero(t, transport.edits)
			assert.Equal(t, 1, transport.reads)
			assert.Equal(t, before.Components, lookupSnapshot(t, f.helper.Components).Components)
			assert.Equal(t, 1, f.feedPosts)
			assert.Zero(t, f.helperPosts)
		})
	}
}

func TestBumpCommandReportsHelperWarningAsSuccess(t *testing.T) {
	f := newCooldownPostFixture(t)
	transport := stubBumpHelper(t, f)
	legacyBumpHelper(t, f, true, false)
	transport.failEdit = true
	_, cache := forumcache.NewTestForumCache(nil)
	cache.RegisterForum("forum")
	seedThread(cache, "forum", "guild", "user", f.thread.ID)
	f.svc.deps.ForumCache = cache
	module := New(f.svc.deps)
	module.Register(map[string]*types.Command{}, f.svc.deps)
	interaction := buildInteraction("guild", "user")
	interaction.ChannelID = "forum"
	capture := &hookCapture{}
	withHooks(t, capture, func() { module.handleBumpIntro(f.svc.deps.Session, interaction) })
	assert.Contains(t, capture.lastEdit, "✅ Your introduction has been posted to the feed!")
	assert.Contains(t, capture.lastEdit, "⚠️")
	assert.NotContains(t, capture.lastEdit, "❌")
	assert.NotContains(t, capture.lastEdit, "try again")
	assert.Equal(t, 1, f.feedPosts)
	assert.Equal(t, 1, transport.edits)
}

func TestBumpRefreshFindsHelperAmongUnrelatedReplies(t *testing.T) {
	f := newCooldownPostFixture(t)
	transport := stubBumpHelper(t, f)
	legacyBumpHelper(t, f, true, true)
	transport.replies = []*discordgo.Message{
		{ID: "human", ChannelID: f.thread.ID, Author: &discordgo.User{ID: "user"}, Content: "hello"},
		{ID: "other-comment", ChannelID: f.thread.ID, Author: f.svc.deps.Session.State.User, Components: []discordgo.MessageComponent{&discordgo.TextDisplay{Content: "Another bot comment"}}},
		f.helper,
	}
	warning, err := f.svc.BumpIntroToFeed("guild", f.thread.ID, "user", "User", "Intro", false)
	require.NoError(t, err)
	assert.Empty(t, warning, f.logs.String())
	assert.Equal(t, 1, transport.reads)
	assert.Equal(t, 1, transport.edits)
	assert.Equal(t, "Another bot comment", transport.replies[1].Components[0].(*discordgo.TextDisplay).Content)
}
