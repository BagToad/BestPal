package say

import (
	"fmt"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ScheduledMessage represents an in-memory scheduled anonymous message
type ScheduledMessage struct {
	ID                 int64
	ChannelID          string
	Content            string
	FireAt             time.Time
	ScheduledBy        string // user ID of moderator
	SuppressModMessage bool
}

// Service holds scheduled messages in memory only
type Service struct {
	types.BaseService
	cfg      *config.Config
	mu       sync.Mutex
	messages []ScheduledMessage
	nextID   atomic.Int64
}

// NewService creates a new say service
func NewService(cfg *config.Config) *Service {
	svc := &Service{cfg: cfg, messages: make([]ScheduledMessage, 0, 16)}
	svc.nextID.Store(1)
	return svc
}

// SetSession sets the Discord session for the service (deprecated, use HydrateServiceDiscordSession)
func (s *Service) SetSession(session *discordgo.Session) {
	s.Session = session
}

// Add inserts a new scheduled message (kept in-memory only)
func (s *Service) Add(msg ScheduledMessage) int64 {
	msg.ID = s.nextID.Add(1) - 1
	s.mu.Lock()
	s.messages = append(s.messages, msg)
	// keep slice ordered by FireAt ascending for efficient checks
	sort.SliceStable(s.messages, func(i, j int) bool { return s.messages[i].FireAt.Before(s.messages[j].FireAt) })
	s.mu.Unlock()
	return msg.ID
}

// List returns up to limit upcoming scheduled messages (sorted soonest first)
func (s *Service) List(limit int) []ScheduledMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit > len(s.messages) {
		limit = len(s.messages)
	}
	out := make([]ScheduledMessage, limit)
	copy(out, s.messages[:limit])
	return out
}

// Cancel removes a scheduled message by ID; returns true if removed
func (s *Service) Cancel(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for idx, m := range s.messages {
		if m.ID == id {
			s.messages = append(s.messages[:idx], s.messages[idx+1:]...)
			return true
		}
	}
	return false
}

// CheckAndSendDue sends all messages whose FireAt <= now.
// It returns an error aggregating any send failures.
func (s *Service) CheckAndSendDue(session *discordgo.Session) error {
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
		logMsg := fmt.Sprintf("[ScheduledSay Fired]\nID: %d\nChannel: %s\nModerator: %s\nFire At: %s (<t:%d:F>)\nDiscord Msg ID: %s\nSuppress Footer: %v\nPreview: %.10q", m.ID, m.ChannelID, m.ScheduledBy, m.FireAt.UTC().Format(time.RFC3339), m.FireAt.Unix(), sent.ID, m.SuppressModMessage, m.Content)
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

// CheckDue checks and sends due scheduled messages using the stored session
func (s *Service) CheckDue() error {
	if s.Session == nil {
		return fmt.Errorf("session not initialized")
	}
	return s.CheckAndSendDue(s.Session)
}

// ScheduledFuncs returns functions to be called on a schedule
func (s *Service) ScheduledFuncs() map[string]func() error {
	return map[string]func() error{
		"@every 1m": s.CheckDue,
	}
}

// helper for inline min without pulling math
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
