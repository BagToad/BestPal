package commands

import (
	"fmt"
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"sort"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ScheduledMessage represents an in-memory scheduled anonymous message
type ScheduledMessage struct {
	ChannelID          string
	Content            string
	FireAt             time.Time
	ScheduledBy        string // user ID of moderator
	SuppressModMessage bool
}

// ScheduleSayService holds scheduled messages in memory only
type ScheduleSayService struct {
	cfg      *config.Config
	mu       sync.Mutex
	messages []ScheduledMessage
}

func NewScheduleSayService(cfg *config.Config) *ScheduleSayService {
	return &ScheduleSayService{cfg: cfg, messages: make([]ScheduledMessage, 0, 16)}
}

// Add inserts a new scheduled message (kept in-memory only)
func (s *ScheduleSayService) Add(msg ScheduledMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
	// keep slice ordered by FireAt ascending for efficient checks
	sort.SliceStable(s.messages, func(i, j int) bool { return s.messages[i].FireAt.Before(s.messages[j].FireAt) })
}

// CheckAndSendDue sends all messages whose FireAt <= now.
// It returns an error aggregating any send failures.
func (s *ScheduleSayService) CheckAndSendDue(session *discordgo.Session) error {
	now := time.Now()
	var due []ScheduledMessage

	s.mu.Lock()
	// find index of first message after now
	idx := 0
	for idx < len(s.messages) && !s.messages[idx].FireAt.After(now) {
		due = append(due, s.messages[idx])
		idx++
	}
	if idx > 0 {
		// trim executed messages
		s.messages = append([]ScheduledMessage(nil), s.messages[idx:]...)
	}
	s.mu.Unlock()

	if len(due) == 0 {
		return nil
	}

	var errs []error
	for _, m := range due {
		content := m.Content
		if !m.SuppressModMessage {
			content = fmt.Sprintf("%s\n\n**On behalf of moderator**", content)
		}
		sent, err := session.ChannelMessageSend(m.ChannelID, content)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed sending scheduled message to channel %s: %w", m.ChannelID, err))
			continue
		}
		logMsg := fmt.Sprintf("ScheduledSay Fired: moderator=%s channel=%s fire_at=%s message_id=%s", m.ScheduledBy, m.ChannelID, m.FireAt.UTC().Format(time.RFC3339), sent.ID)
		if lErr := utils.LogToChannel(s.cfg, session, logMsg); lErr != nil {
			s.cfg.Logger.Errorf("failed logging scheduled say fire: %v", lErr)
		}
		s.cfg.Logger.Info(logMsg)
	}

	if len(errs) > 0 {
		return fmt.Errorf("%d scheduled send errors (first: %v)", len(errs), errs[0])
	}
	return nil
}
