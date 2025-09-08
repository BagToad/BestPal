package lfgpanel

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
)

type Entry struct {
	UserID      string
	ThreadID    string
	Region      string
	Message     string
	PlayerCount int
	UpdatedAt   time.Time
}

// Service defines the behaviour required by the slash command layer.
type Service interface {
	Upsert(threadID, userID, region, message string, playerCount int) error
	RefreshPanel(s discordSessionLike, ttl time.Duration) error
	SetupPanel(channelID string)
	PanelChannel() string
}

// discordSessionLike matches subset of discordgo.Session methods (including variadic options) so a real *discordgo.Session satisfies it.
type discordSessionLike interface {
	Channel(id string, options ...discordgo.RequestOption) (*discordgo.Channel, error)
	ChannelMessageDelete(channelID, messageID string, options ...discordgo.RequestOption) error
	ChannelMessageEditComplex(m *discordgo.MessageEdit, options ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelMessageSendEmbeds(channelID string, embeds []*discordgo.MessageEmbed, options ...discordgo.RequestOption) (*discordgo.Message, error)
}

// ChannelFetcher allows injecting a lightweight abstraction for fetching channels (enables test stubbing).
type ChannelFetcher interface {
	Channel(id string) (*discordgo.Channel, error)
	ChannelMessageDelete(channelID, messageID string) error
	ChannelMessageEditComplex(m *discordgo.MessageEdit) (*discordgo.Message, error)
	ChannelMessageSendEmbeds(channelID string, embeds []*discordgo.MessageEmbed) (*discordgo.Message, error)
}

// NewLFGPanelService creates a new in-memory Service implementation and auto-loads persisted panel channel ID.
func NewLFGPanelService(cfg *config.Config) *InMemoryService {
	s := &InMemoryService{
		entries: make(map[string]map[string]*Entry),
		cfg:     cfg,
		logf:    func(string, ...any) {},
		warnf:   func(string, ...any) {},
	}
	if cfg != nil {
		if chID := cfg.GetLFGNowPanelChannelID(); chID != "" {
			s.panelChannelID = chID
		}
	}
	return s
}

// InMemoryService is the default implementation storing state in memory.
type InMemoryService struct {
	mu              sync.RWMutex
	entries         map[string]map[string]*Entry // threadID -> userID -> entry
	panelChannelID  string
	panelMessageIDs []string
	cfg             *config.Config
	logf            func(msg string, args ...any)
	warnf           func(msg string, args ...any)
}

// WithLogger wires optional logger funcs.
func (s *InMemoryService) WithLogger(logf, warnf func(string, ...any)) *InMemoryService {
	s.logf = logf
	s.warnf = warnf
	return s
}

// PanelChannel returns current panel channel id.
func (s *InMemoryService) PanelChannel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.panelChannelID
}

// SetupPanel sets the channel for the panel and clears existing messages.
func (s *InMemoryService) SetupPanel(channelID string) {
	s.mu.Lock()
	s.panelChannelID = channelID
	s.panelMessageIDs = nil
	cfg := s.cfg
	s.mu.Unlock()
	if cfg != nil {
		cfg.Set("gamerpals_lfg_now_panel_channel_id", channelID)
	}
}

// Upsert creates/updates a user entry.
func (s *InMemoryService) Upsert(threadID, userID, region, message string, playerCount int) error {
	if threadID == "" || userID == "" {
		return fmt.Errorf("missing threadID or userID")
	}
	if playerCount <= 0 || playerCount > 99 {
		return fmt.Errorf("invalid playerCount")
	}
	if len(message) > 140 {
		message = message[:137] + "..."
	}
	region = strings.ToUpper(region)
	s.mu.Lock()
	perThread := s.entries[threadID]
	if perThread == nil {
		perThread = make(map[string]*Entry)
		s.entries[threadID] = perThread
	}
	perThread[userID] = &Entry{UserID: userID, ThreadID: threadID, Region: region, Message: message, PlayerCount: playerCount, UpdatedAt: time.Now()}
	s.mu.Unlock()
	return nil
}

// RefreshPanel rebuilds embeds and updates/creates messages.
func (s *InMemoryService) RefreshPanel(sess discordSessionLike, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.panelChannelID == "" {
		return nil
	}
	cutoff := time.Now().Add(-ttl)
	// prune
	for threadID, users := range s.entries {
		for uid, e := range users {
			if e.UpdatedAt.Before(cutoff) {
				delete(users, uid)
			}
		}
		if len(users) == 0 {
			delete(s.entries, threadID)
		}
	}
	// build sections
	type section struct {
		Name  string
		Lines []string
	}
	var secs []section
	for threadID, users := range s.entries {
		if len(users) == 0 {
			continue
		}
		ch, err := sess.Channel(threadID)
		if err != nil || ch == nil {
			continue
		}
		var lines []string
		for _, e := range users {
			exp := e.UpdatedAt.Add(ttl).Unix()
			playersDescriber := "pals"
			if e.PlayerCount == 1 {
				playersDescriber = "pal"
			}
			lines = append(lines, fmt.Sprintf("<@%s> [%s] (looking for %d %s) - %s (expires <t:%d:R>)", e.UserID, e.Region, e.PlayerCount, playersDescriber, e.Message, exp))
		}
		sort.Strings(lines)
		secs = append(secs, section{Name: ch.Name, Lines: lines})
	}
	sort.Slice(secs, func(i, j int) bool { return secs[i].Name < secs[j].Name })
	var embeds []*discordgo.MessageEmbed
	cur := &discordgo.MessageEmbed{Title: "Looking NOW"}
	for _, sec := range secs {
		val := strings.Join(sec.Lines, "\n")
		if len(val) > 1024 {
			val = val[:1019] + "..."
		}
		if len(cur.Fields) >= 25 {
			embeds = append(embeds, cur)
			cur = &discordgo.MessageEmbed{Title: "Looking NOW (cont)"}
		}
		cur.Fields = append(cur.Fields, &discordgo.MessageEmbedField{Name: sec.Name, Value: val})
	}
	if len(cur.Fields) > 0 || len(embeds) == 0 {
		embeds = append(embeds, cur)
	}
	if len(embeds) > 0 {
		embeds[len(embeds)-1].Footer = &discordgo.MessageEmbedFooter{Text: "Run `/lfg now` in any game thread"}
	}
	if len(secs) == 0 { // show empty state embed instead of clearing everything
		emptyEmbed := &discordgo.MessageEmbed{
			Title:       "Looking NOW",
			Description: "Nobody is on right now :zzz:",
			Footer:      &discordgo.MessageEmbedFooter{Text: "Run `/lfg now` in any game thread"},
		}
		if len(s.panelMessageIDs) > 0 {
			first := s.panelMessageIDs[0]
			_, _ = sess.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: first, Channel: s.panelChannelID, Embeds: &[]*discordgo.MessageEmbed{emptyEmbed}})
			// delete any excess previous embeds
			for _, mid := range s.panelMessageIDs[1:] {
				_ = sess.ChannelMessageDelete(s.panelChannelID, mid)
			}
			// keep only the first ID
			s.panelMessageIDs = s.panelMessageIDs[:1]
		} else { // none existed previously, create new
			msg, err := sess.ChannelMessageSendEmbeds(s.panelChannelID, []*discordgo.MessageEmbed{emptyEmbed})
			if err == nil {
				s.panelMessageIDs = []string{msg.ID}
			}
		}
		return nil
	}
	if len(s.panelMessageIDs) == len(embeds) {
		for i, mid := range s.panelMessageIDs {
			_, _ = sess.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: mid, Channel: s.panelChannelID, Embeds: &[]*discordgo.MessageEmbed{embeds[i]}})
		}
		return nil
	}
	for _, mid := range s.panelMessageIDs {
		_ = sess.ChannelMessageDelete(s.panelChannelID, mid)
	}
	var newIDs []string
	for _, emb := range embeds {
		msg, err := sess.ChannelMessageSendEmbeds(s.panelChannelID, []*discordgo.MessageEmbed{emb})
		if err == nil {
			newIDs = append(newIDs, msg.ID)
		}
	}
	s.panelMessageIDs = newIDs
	return nil
}
