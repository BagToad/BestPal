// Package eventfeed posts server scheduled events to the event feed channel:
// once when an event is created, and as two configurable reminders before it
// starts (1 hour and 1 day before by default).
package eventfeed

import (
	"fmt"
	"strings"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"

	"github.com/MakeNowJust/heredoc"
	"github.com/bwmarrin/discordgo"
)

// reminderRetention is how long handled-reminder records are kept. Anything
// older belongs to an occurrence that has long since started.
const reminderRetention = 30 * 24 * time.Hour

// Reminder slots as stored in the database.
const (
	slotNotification1 = "notification_1"
	slotNotification2 = "notification_2"
)

type store interface {
	EventReminderSent(guildID, eventID, slot string, start time.Time) (bool, error)
	MarkEventReminderSent(guildID, eventID, slot string, start, now time.Time) error
	PruneEventReminders(before time.Time) (int64, error)
}

type discordOps struct {
	ScheduledEvents func(s *discordgo.Session, guildID string) ([]*discordgo.GuildScheduledEvent, error)
	SendMessage     func(s *discordgo.Session, channelID, content string) error
}

func defaultDiscordOps() discordOps {
	return discordOps{
		ScheduledEvents: func(s *discordgo.Session, guildID string) ([]*discordgo.GuildScheduledEvent, error) {
			return s.GuildScheduledEvents(guildID, false)
		},
		SendMessage: func(s *discordgo.Session, channelID, content string) error {
			_, err := s.ChannelMessageSend(channelID, content)
			return err
		},
	}
}

// Module owns the event feed.
type Module struct {
	config  *config.Config
	store   store
	ops     discordOps
	session *discordgo.Session
	service *Service
	now     func() time.Time
}

// New creates the event feed module.
func New(deps *types.Dependencies) *Module {
	m := &Module{
		config:  deps.Config,
		ops:     defaultDiscordOps(),
		session: deps.Session,
		now:     time.Now,
	}
	if deps.DB != nil {
		m.store = deps.DB
	}
	m.service = &Service{m: m}
	return m
}

// Register adds no commands; the feed is driven by gateway events and the
// scheduler.
func (m *Module) Register(map[string]*types.Command, *types.Dependencies) {}

// Service returns the module's scheduler-backed service.
func (m *Module) Service() types.ModuleService { return m.service }

// ConfigSettings declares the event feed's per-guild settings.
func (m *Module) ConfigSettings() []config.Setting {
	return []config.Setting{
		{
			Key:         config.KeyEventFeedEnabled,
			Category:    config.CategoryEvents,
			Label:       "Event feed",
			Description: "Master switch for event feed posts: new events and both reminders.",
			Kind:        config.KindBool,
			Default:     true,
		},
		{
			Key:         config.KeyEventFeedChannelID,
			Category:    config.CategoryEvents,
			Label:       "Event feed channel",
			Description: "Channel where new server events and their reminders are posted.",
			Kind:        config.KindChannel,
		},
		{
			Key:         config.KeyEventFeedNotification1,
			Category:    config.CategoryEvents,
			Label:       "Notification 1",
			Description: "How long before an event starts to post the first reminder (e.g. 1h). 0 turns it off.",
			Kind:        config.KindDuration,
			Default:     "1h",
			Validate:    validateLeadTime,
		},
		{
			Key:         config.KeyEventFeedNotification2,
			Category:    config.CategoryEvents,
			Label:       "Notification 2",
			Description: "How long before an event starts to post the second reminder (e.g. 24h). 0 turns it off.",
			Kind:        config.KindDuration,
			Default:     "24h",
			Validate:    validateLeadTime,
		},
	}
}

func validateLeadTime(raw string) error {
	if err := config.ValidateDuration(raw); err != nil {
		return err
	}
	d, _ := time.ParseDuration(strings.TrimSpace(raw))
	if d < 0 {
		return fmt.Errorf("must be zero or greater")
	}
	return nil
}

func eventURL(guildID, eventID string) string {
	return fmt.Sprintf("https://discord.com/events/%s/%s", guildID, eventID)
}

// OnGuildScheduledEventCreate posts newly created events to the feed channel.
func (m *Module) OnGuildScheduledEventCreate(s *discordgo.Session, e *discordgo.GuildScheduledEventCreate) {
	if e == nil || e.GuildScheduledEvent == nil {
		return
	}
	serverID := m.config.GetGamerPalsServerID()
	if serverID != "" && e.GuildID != serverID {
		return
	}
	gc := m.config.ForGuild(e.GuildID)
	if !gc.GetEventFeedEnabled() {
		return
	}
	channelID := gc.GetEventFeedChannelID()
	if channelID == "" {
		return
	}

	msg := heredoc.Docf(`
		New event created! [Click here to view details](%s)
	`, eventURL(e.GuildID, e.ID))

	if err := m.ops.SendMessage(s, channelID, msg); err != nil {
		m.config.Logger.Errorf("Failed to send event feed message: %v", err)
	}
}

type reminderSlot struct {
	name string
	lead time.Duration
}

// reminderTick posts any reminders that are due for upcoming events.
//
// A slot is due once now >= start - lead and the event hasn't started. Slots
// whose trigger time is before the event was created are skipped, since the
// "new event" post already covers them. If the bot was down and several slots
// are due at once, only the one closest to the start is posted and the rest
// are marked handled. Handled slots are recorded per start time in the
// database, so restarts don't repeat them and reschedules re-arm them.
func (m *Module) reminderTick() error {
	if m.session == nil || m.store == nil {
		return nil
	}
	guildID := m.config.GetGamerPalsServerID()
	if guildID == "" {
		return nil
	}
	gc := m.config.ForGuild(guildID)
	if !gc.GetEventFeedEnabled() {
		return nil
	}
	channelID := gc.GetEventFeedChannelID()
	if channelID == "" {
		return nil
	}

	var slots []reminderSlot
	if d := gc.GetEventFeedNotification1(); d > 0 {
		slots = append(slots, reminderSlot{slotNotification1, d})
	}
	if d := gc.GetEventFeedNotification2(); d > 0 {
		slots = append(slots, reminderSlot{slotNotification2, d})
	}
	if len(slots) == 0 {
		return nil
	}

	events, err := m.ops.ScheduledEvents(m.session, guildID)
	if err != nil {
		return fmt.Errorf("event feed: list scheduled events: %w", err)
	}

	now := m.now()
	for _, ev := range events {
		if err := m.remindEvent(guildID, channelID, ev, slots, now); err != nil {
			m.config.Logger.Errorf("event feed: reminder for event %s: %v", ev.ID, err)
		}
	}

	if _, err := m.store.PruneEventReminders(now.Add(-reminderRetention)); err != nil {
		return err
	}
	return nil
}

func (m *Module) remindEvent(guildID, channelID string, ev *discordgo.GuildScheduledEvent, slots []reminderSlot, now time.Time) error {
	if ev == nil || ev.Status != discordgo.GuildScheduledEventStatusScheduled {
		return nil
	}
	start := ev.ScheduledStartTime
	if !now.Before(start) {
		return nil
	}
	created, err := discordgo.SnowflakeTimestamp(ev.ID)
	if err != nil {
		created = time.Time{}
	}

	var due []reminderSlot
	var closest *reminderSlot
	for i, sl := range slots {
		trigger := start.Add(-sl.lead)
		if now.Before(trigger) || trigger.Before(created) {
			continue
		}
		sent, err := m.store.EventReminderSent(guildID, ev.ID, sl.name, start)
		if err != nil {
			return err
		}
		if sent {
			continue
		}
		due = append(due, sl)
		if closest == nil || sl.lead < closest.lead {
			closest = &slots[i]
		}
	}
	if closest == nil {
		return nil
	}

	if err := m.ops.SendMessage(m.session, channelID, reminderMessage(guildID, ev)); err != nil {
		return fmt.Errorf("send reminder: %w", err)
	}
	for _, sl := range due {
		if err := m.store.MarkEventReminderSent(guildID, ev.ID, sl.name, start, now); err != nil {
			return err
		}
	}
	return nil
}

func reminderMessage(guildID string, ev *discordgo.GuildScheduledEvent) string {
	ts := ev.ScheduledStartTime.Unix()
	return heredoc.Docf(`
		⏰ **%s** starts <t:%d:R> (<t:%d:f>)! [Click here to view details](%s)
	`, ev.Name, ts, ts, eventURL(guildID, ev.ID))
}

// Service runs the reminder check on the scheduler.
type Service struct {
	m *Module
}

// HydrateServiceDiscordSession stores the session once it is connected.
func (s *Service) HydrateServiceDiscordSession(session *discordgo.Session) error {
	s.m.session = session
	return nil
}

// ScheduledFuncs checks for due reminders every minute.
func (s *Service) ScheduledFuncs() map[string]func() error {
	return map[string]func() error{
		"@every 1m": s.m.reminderTick,
	}
}
