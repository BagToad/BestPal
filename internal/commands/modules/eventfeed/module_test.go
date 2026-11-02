package eventfeed

import (
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/require"
)

var testNow = time.Unix(1_800_000_000, 0)

type sent struct {
	channelID string
	content   string
}

type harness struct {
	m      *Module
	db     *database.DB
	sent   []sent
	events []*discordgo.GuildScheduledEvent
	now    time.Time
}

func newHarness(t *testing.T, kv map[string]any) *harness {
	t.Helper()
	db, err := database.NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	base := map[string]any{
		"gamerpals_server_id":       "guild1",
		"event_feed_channel_id":     "feed",
		"event_feed_enabled":        true,
		"event_feed_notification_1": "1h",
		"event_feed_notification_2": "24h",
	}
	for k, v := range kv {
		base[k] = v
	}

	h := &harness{db: db, now: testNow}
	h.m = &Module{
		config:  config.NewMockConfig(base),
		store:   db,
		session: &discordgo.Session{},
		now:     func() time.Time { return h.now },
		ops: discordOps{
			ScheduledEvents: func(_ *discordgo.Session, guildID string) ([]*discordgo.GuildScheduledEvent, error) {
				return h.events, nil
			},
			SendMessage: func(_ *discordgo.Session, channelID, content string) error {
				h.sent = append(h.sent, sent{channelID, content})
				return nil
			},
		},
	}
	h.m.service = &Service{m: h.m}
	return h
}

// snowflake builds an ID whose embedded creation time is created.
func snowflake(created time.Time, seq int64) string {
	ms := created.UnixMilli() - 1420070400000
	return strconv.FormatInt(ms<<22|seq, 10)
}

func event(created, start time.Time, seq int64) *discordgo.GuildScheduledEvent {
	return &discordgo.GuildScheduledEvent{
		ID:                 snowflake(created, seq),
		GuildID:            "guild1",
		Name:               "Game night",
		ScheduledStartTime: start,
		Status:             discordgo.GuildScheduledEventStatusScheduled,
	}
}

func TestCreatePostsToFeed(t *testing.T) {
	h := newHarness(t, nil)
	ev := event(testNow, testNow.Add(48*time.Hour), 1)
	h.m.OnGuildScheduledEventCreate(nil, &discordgo.GuildScheduledEventCreate{GuildScheduledEvent: ev})
	require.Len(t, h.sent, 1)
	require.Equal(t, "feed", h.sent[0].channelID)
	require.Contains(t, h.sent[0].content, "New event created!")
	require.Contains(t, h.sent[0].content, "https://discord.com/events/guild1/"+ev.ID)
}

func TestCreateRespectsEnabledAndGuild(t *testing.T) {
	h := newHarness(t, map[string]any{"event_feed_enabled": false})
	h.m.OnGuildScheduledEventCreate(nil, &discordgo.GuildScheduledEventCreate{GuildScheduledEvent: event(testNow, testNow.Add(time.Hour), 1)})
	require.Empty(t, h.sent)

	h = newHarness(t, nil)
	ev := event(testNow, testNow.Add(time.Hour), 1)
	ev.GuildID = "other"
	h.m.OnGuildScheduledEventCreate(nil, &discordgo.GuildScheduledEventCreate{GuildScheduledEvent: ev})
	require.Empty(t, h.sent)

	h = newHarness(t, map[string]any{"event_feed_channel_id": ""})
	h.m.OnGuildScheduledEventCreate(nil, &discordgo.GuildScheduledEventCreate{GuildScheduledEvent: event(testNow, testNow.Add(time.Hour), 1)})
	require.Empty(t, h.sent)
}

func TestRemindersFireOnceAtEachLeadTime(t *testing.T) {
	h := newHarness(t, nil)
	created := testNow.Add(-72 * time.Hour)
	start := testNow.Add(48 * time.Hour)
	ev := event(created, start, 1)
	h.events = []*discordgo.GuildScheduledEvent{ev}

	// Too early for either reminder.
	require.NoError(t, h.m.reminderTick())
	require.Empty(t, h.sent)

	// One day before.
	h.now = start.Add(-24 * time.Hour)
	require.NoError(t, h.m.reminderTick())
	require.Len(t, h.sent, 1)
	require.Contains(t, h.sent[0].content, "Game night")
	require.Contains(t, h.sent[0].content, "<t:"+strconv.FormatInt(start.Unix(), 10)+":R>")

	// Ticking again (or after a restart) doesn't repeat it.
	h.now = h.now.Add(time.Minute)
	require.NoError(t, h.m.reminderTick())
	require.Len(t, h.sent, 1)

	// One hour before.
	h.now = start.Add(-time.Hour)
	require.NoError(t, h.m.reminderTick())
	require.Len(t, h.sent, 2)

	h.now = start.Add(-30 * time.Minute)
	require.NoError(t, h.m.reminderTick())
	require.Len(t, h.sent, 2)

	// Started: nothing more.
	h.now = start.Add(time.Minute)
	require.NoError(t, h.m.reminderTick())
	require.Len(t, h.sent, 2)
}

func TestRemindersSurviveRestart(t *testing.T) {
	h := newHarness(t, nil)
	start := testNow.Add(time.Hour)
	h.events = []*discordgo.GuildScheduledEvent{event(testNow.Add(-72*time.Hour), start, 1)}
	require.NoError(t, h.m.reminderTick())
	require.Len(t, h.sent, 1)

	// A fresh module on the same database (a bot restart) doesn't resend.
	restarted := &Module{config: h.m.config, store: h.db, session: h.m.session, now: h.m.now, ops: h.m.ops}
	require.NoError(t, restarted.reminderTick())
	require.Len(t, h.sent, 1)
}

func TestMissedRemindersCollapseToOne(t *testing.T) {
	// Bot was down for both trigger times: post once, mark both handled.
	h := newHarness(t, nil)
	start := testNow.Add(30 * time.Minute)
	h.events = []*discordgo.GuildScheduledEvent{event(testNow.Add(-72*time.Hour), start, 1)}
	require.NoError(t, h.m.reminderTick())
	require.Len(t, h.sent, 1)
	h.now = h.now.Add(time.Minute)
	require.NoError(t, h.m.reminderTick())
	require.Len(t, h.sent, 1)
}

func TestNoReminderForWindowsBeforeCreation(t *testing.T) {
	// Created 2h before start: the 1d reminder is skipped, the 1h one still fires.
	h := newHarness(t, nil)
	start := testNow.Add(2 * time.Hour)
	h.events = []*discordgo.GuildScheduledEvent{event(testNow, start, 1)}
	require.NoError(t, h.m.reminderTick())
	require.Empty(t, h.sent)

	h.now = start.Add(-time.Hour)
	require.NoError(t, h.m.reminderTick())
	require.Len(t, h.sent, 1)
}

func TestRescheduleRearmsReminders(t *testing.T) {
	h := newHarness(t, nil)
	ev := event(testNow.Add(-72*time.Hour), testNow.Add(time.Hour), 1)
	h.events = []*discordgo.GuildScheduledEvent{ev}
	require.NoError(t, h.m.reminderTick())
	require.Len(t, h.sent, 1)

	// Moved a week later (or a recurring event's next occurrence).
	ev.ScheduledStartTime = testNow.Add(7 * 24 * time.Hour)
	h.now = ev.ScheduledStartTime.Add(-24 * time.Hour)
	require.NoError(t, h.m.reminderTick())
	require.Len(t, h.sent, 2)
	h.now = ev.ScheduledStartTime.Add(-time.Hour)
	require.NoError(t, h.m.reminderTick())
	require.Len(t, h.sent, 3)
}

func TestConfigurableLeadTimes(t *testing.T) {
	h := newHarness(t, map[string]any{"event_feed_notification_1": "3h", "event_feed_notification_2": "0"})
	start := testNow.Add(48 * time.Hour)
	h.events = []*discordgo.GuildScheduledEvent{event(testNow.Add(-72*time.Hour), start, 1)}

	// Notification 2 is off, so nothing a day before.
	h.now = start.Add(-24 * time.Hour)
	require.NoError(t, h.m.reminderTick())
	require.Empty(t, h.sent)

	h.now = start.Add(-3*time.Hour - time.Minute)
	require.NoError(t, h.m.reminderTick())
	require.Empty(t, h.sent)

	h.now = start.Add(-3 * time.Hour)
	require.NoError(t, h.m.reminderTick())
	require.Len(t, h.sent, 1)
}

func TestRemindersSkipped(t *testing.T) {
	start := testNow.Add(30 * time.Minute)
	created := testNow.Add(-72 * time.Hour)

	t.Run("feature disabled", func(t *testing.T) {
		h := newHarness(t, map[string]any{"event_feed_enabled": false})
		h.events = []*discordgo.GuildScheduledEvent{event(created, start, 1)}
		require.NoError(t, h.m.reminderTick())
		require.Empty(t, h.sent)
	})
	t.Run("no channel", func(t *testing.T) {
		h := newHarness(t, map[string]any{"event_feed_channel_id": ""})
		h.events = []*discordgo.GuildScheduledEvent{event(created, start, 1)}
		require.NoError(t, h.m.reminderTick())
		require.Empty(t, h.sent)
	})
	t.Run("not scheduled", func(t *testing.T) {
		h := newHarness(t, nil)
		ev := event(created, start, 1)
		ev.Status = discordgo.GuildScheduledEventStatusCanceled
		h.events = []*discordgo.GuildScheduledEvent{ev}
		require.NoError(t, h.m.reminderTick())
		require.Empty(t, h.sent)
	})
}

func TestFailedSendRetries(t *testing.T) {
	h := newHarness(t, nil)
	h.events = []*discordgo.GuildScheduledEvent{event(testNow.Add(-72*time.Hour), testNow.Add(time.Hour), 1)}
	send := h.m.ops.SendMessage
	h.m.ops.SendMessage = func(*discordgo.Session, string, string) error { return errors.New("boom") }
	require.NoError(t, h.m.reminderTick())
	require.Empty(t, h.sent)

	h.m.ops.SendMessage = send
	h.now = h.now.Add(time.Minute)
	require.NoError(t, h.m.reminderTick())
	require.Len(t, h.sent, 1)
}

func TestValidateLeadTime(t *testing.T) {
	require.NoError(t, validateLeadTime("1h"))
	require.NoError(t, validateLeadTime("0"))
	require.Error(t, validateLeadTime("-1h"))
	require.Error(t, validateLeadTime("soon"))
}

func TestFeedMessageSuppressesMentions(t *testing.T) {
	msg := feedMessage("@everyone starts soon")
	require.NotNil(t, msg.AllowedMentions)
	require.NotNil(t, msg.AllowedMentions.Parse)
	require.Empty(t, msg.AllowedMentions.Parse)
	require.Empty(t, msg.AllowedMentions.Users)
	require.Empty(t, msg.AllowedMentions.Roles)
}
