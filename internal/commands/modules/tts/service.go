package tts

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/voice"

	"github.com/bwmarrin/discordgo"
)

const (
	// joinTimeout bounds how long we wait for Discord to deliver the voice
	// server and state updates after requesting to join.
	joinTimeout = 15 * time.Second
)

// ErrBusy is returned when the bot is already speaking in the target guild.
var ErrBusy = errors.New("the bot is already speaking in this server, try again in a moment")

// pendingJoin collects the two gateway events a voice handshake needs: the voice
// server update (token + endpoint) and the bot's own voice state update
// (session id). Channels are buffered so the event handlers never block.
type pendingJoin struct {
	serverCh chan *discordgo.VoiceServerUpdate
	stateCh  chan *discordgo.VoiceStateUpdate
}

// Service owns the synthesizer and orchestrates per-guild voice joins for /tts.
type Service struct {
	types.BaseService
	cfg   *config.Config
	synth Synthesizer

	mu      sync.Mutex
	pending map[string]*pendingJoin
	locks   map[string]*sync.Mutex
}

// NewService builds the TTS service with the offline, pure-Go synthesizer.
func NewService(cfg *config.Config) *Service {
	return &Service{
		cfg:     cfg,
		synth:   newOfflineSynthesizer(),
		pending: make(map[string]*pendingJoin),
		locks:   make(map[string]*sync.Mutex),
	}
}

// ScheduledFuncs implements types.ModuleService; TTS has no scheduled work.
func (s *Service) ScheduledFuncs() map[string]func() error { return nil }

// guildLock returns the per-guild mutex used to serialize voice sessions.
func (s *Service) guildLock(guildID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, ok := s.locks[guildID]
	if !ok {
		lock = &sync.Mutex{}
		s.locks[guildID] = lock
	}
	return lock
}

func (s *Service) setPending(guildID string, pj *pendingJoin) {
	s.mu.Lock()
	s.pending[guildID] = pj
	s.mu.Unlock()
}

func (s *Service) clearPending(guildID string) {
	s.mu.Lock()
	delete(s.pending, guildID)
	s.mu.Unlock()
}

func (s *Service) getPending(guildID string) *pendingJoin {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending[guildID]
}

// OnVoiceServerUpdate routes the voice server update to a waiting join, if any.
func (s *Service) OnVoiceServerUpdate(_ *discordgo.Session, e *discordgo.VoiceServerUpdate) {
	pj := s.getPending(e.GuildID)
	if pj == nil {
		return
	}
	select {
	case pj.serverCh <- e:
	default:
	}
}

// OnVoiceStateUpdate routes the bot's own voice state update to a waiting join.
func (s *Service) OnVoiceStateUpdate(sess *discordgo.Session, e *discordgo.VoiceStateUpdate) {
	if e.VoiceState == nil {
		return
	}
	if sess.State == nil || sess.State.User == nil || e.UserID != sess.State.User.ID {
		return
	}
	pj := s.getPending(e.GuildID)
	if pj == nil {
		return
	}
	select {
	case pj.stateCh <- e:
	default:
	}
}

// Speak synthesizes text, joins the given voice channel, plays the audio, and
// leaves. It serializes per guild and returns ErrBusy if a session is already in
// progress there.
func (s *Service) Speak(ctx context.Context, guildID, channelID, text string) error {
	if s.Session == nil {
		return fmt.Errorf("tts: discord session not initialized")
	}

	// Synthesize first so we fail fast before joining the channel.
	frames, err := s.synth.Synthesize(text)
	if err != nil {
		return err
	}
	if len(frames) == 0 {
		return fmt.Errorf("tts: no audio produced")
	}

	lock := s.guildLock(guildID)
	if !lock.TryLock() {
		return ErrBusy
	}
	defer lock.Unlock()

	creds, err := s.join(ctx, guildID, channelID)
	if err != nil {
		return err
	}
	// Always leave the channel when done.
	defer func() { _ = s.Session.ChannelVoiceJoinManual(guildID, "", false, false) }()

	conn, err := voice.Dial(ctx, creds, s.cfg.Logger)
	if err != nil {
		return fmt.Errorf("tts: connect voice: %w", err)
	}
	defer conn.Close()

	if err := conn.Play(ctx, frames); err != nil {
		return fmt.Errorf("tts: play audio: %w", err)
	}
	return nil
}

// join requests the voice channel via the main gateway and waits for the server
// and state updates needed to open the voice connection.
func (s *Service) join(ctx context.Context, guildID, channelID string) (voice.Credentials, error) {
	pj := &pendingJoin{
		serverCh: make(chan *discordgo.VoiceServerUpdate, 1),
		stateCh:  make(chan *discordgo.VoiceStateUpdate, 1),
	}
	s.setPending(guildID, pj)
	defer s.clearPending(guildID)

	// mute=false so we can transmit, deaf=true since we never receive.
	if err := s.Session.ChannelVoiceJoinManual(guildID, channelID, false, true); err != nil {
		return voice.Credentials{}, fmt.Errorf("tts: request voice join: %w", err)
	}

	var (
		server  *discordgo.VoiceServerUpdate
		state   *discordgo.VoiceStateUpdate
		timeout = time.After(joinTimeout)
	)
	for server == nil || server.Endpoint == "" || state == nil {
		select {
		case e := <-pj.serverCh:
			server = e
		case e := <-pj.stateCh:
			state = e
		case <-ctx.Done():
			return voice.Credentials{}, ctx.Err()
		case <-timeout:
			return voice.Credentials{}, fmt.Errorf("tts: timed out joining voice channel")
		}
	}

	return voice.Credentials{
		GuildID:   guildID,
		UserID:    s.Session.State.User.ID,
		SessionID: state.SessionID,
		Token:     server.Token,
		Endpoint:  server.Endpoint,
	}, nil
}
