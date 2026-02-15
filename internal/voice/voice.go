// Package voice provides a bridge between discordgo's gateway and disgo's voice
// implementation. discordgo owns the main gateway WebSocket, while disgo handles
// the voice-specific WebSocket, UDP, and opus framing.
package voice

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	disgoDiscord "github.com/disgoorg/disgo/discord"
	disgoGateway "github.com/disgoorg/disgo/gateway"
	disgovoice "github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
)

// Manager bridges discordgo's gateway with disgo's voice system.
// It translates discordgo events into disgo voice events and sends
// OP4 voice state updates through discordgo's gateway.
type Manager struct {
	mu      sync.Mutex
	session *discordgo.Session
	userID  snowflake.ID
	inner   disgovoice.Manager
	logger  *slog.Logger
}

// NewManager creates a voice Manager that bridges discordgo ↔ disgo voice.
// Call SetSession before use.
func NewManager() *Manager {
	return &Manager{logger: slog.Default()}
}

// SetSession initialises the manager with the discordgo session.
// Must be called once the session is available (e.g. after bot login).
func (m *Manager) SetSession(s *discordgo.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()

	uid, err := snowflake.Parse(s.State.User.ID)
	if err != nil {
		m.logger.Error("voice: failed to parse bot user ID", slog.Any("err", err))
		return
	}
	m.session = s
	m.userID = uid

	// StateUpdateFunc sends OP4 via discordgo's gateway.
	stateUpdate := func(ctx context.Context, guildID snowflake.ID, channelID *snowflake.ID, selfMute bool, selfDeaf bool) error {
		cid := ""
		if channelID != nil {
			cid = channelID.String()
		}
		return s.ChannelVoiceJoinManual(guildID.String(), cid, selfMute, selfDeaf)
	}

	m.inner = disgovoice.NewManager(stateUpdate, uid)
}

// OnVoiceStateUpdate forwards a discordgo VoiceStateUpdate to the disgo voice conn.
func (m *Manager) OnVoiceStateUpdate(vs *discordgo.VoiceStateUpdate) {
	m.mu.Lock()
	inner := m.inner
	m.mu.Unlock()
	if inner == nil {
		return
	}

	guildID, err := snowflake.Parse(vs.GuildID)
	if err != nil {
		return
	}
	userID, err := snowflake.Parse(vs.UserID)
	if err != nil {
		return
	}

	var channelID *snowflake.ID
	if vs.ChannelID != "" {
		cid, err := snowflake.Parse(vs.ChannelID)
		if err != nil {
			return
		}
		channelID = &cid
	}

	inner.HandleVoiceStateUpdate(disgoGateway.EventVoiceStateUpdate{
		VoiceState: disgoVoiceState(guildID, userID, channelID, vs.SessionID),
	})
}

// OnVoiceServerUpdate forwards a discordgo VoiceServerUpdate to the disgo voice conn.
func (m *Manager) OnVoiceServerUpdate(vs *discordgo.VoiceServerUpdate) {
	m.mu.Lock()
	inner := m.inner
	m.mu.Unlock()
	if inner == nil {
		return
	}

	guildID, err := snowflake.Parse(vs.GuildID)
	if err != nil {
		return
	}

	var endpoint *string
	if vs.Endpoint != "" {
		endpoint = &vs.Endpoint
	}

	inner.HandleVoiceServerUpdate(disgoGateway.EventVoiceServerUpdate{
		Token:    vs.Token,
		GuildID:  guildID,
		Endpoint: endpoint,
	})
}

// Join connects to the given voice channel and blocks until ready.
func (m *Manager) Join(ctx context.Context, guildID, channelID string) error {
	m.mu.Lock()
	inner := m.inner
	m.mu.Unlock()
	if inner == nil {
		return fmt.Errorf("voice manager not initialised")
	}

	gid, err := snowflake.Parse(guildID)
	if err != nil {
		return fmt.Errorf("invalid guild ID: %w", err)
	}
	cid, err := snowflake.Parse(channelID)
	if err != nil {
		return fmt.Errorf("invalid channel ID: %w", err)
	}

	// Reuse existing connection if one exists for this guild
	conn := inner.GetConn(gid)
	if conn == nil {
		conn = inner.CreateConn(gid)
	}
	return conn.Open(ctx, cid, false, true) // not muted, deafened
}

// Leave disconnects from voice in the given guild.
func (m *Manager) Leave(ctx context.Context, guildID string) {
	m.mu.Lock()
	inner := m.inner
	m.mu.Unlock()
	if inner == nil {
		return
	}

	gid, err := snowflake.Parse(guildID)
	if err != nil {
		return
	}

	conn := inner.GetConn(gid)
	if conn == nil {
		return
	}
	conn.Close(ctx)
}

// PlaySound plays pre-encoded opus frames (2-byte LE length-prefixed) over
// the voice connection for the given guild. Blocks until playback completes.
func (m *Manager) PlaySound(ctx context.Context, guildID string, opusData []byte) error {
	m.mu.Lock()
	inner := m.inner
	m.mu.Unlock()
	if inner == nil {
		return fmt.Errorf("voice manager not initialised")
	}

	gid, err := snowflake.Parse(guildID)
	if err != nil {
		return fmt.Errorf("invalid guild ID: %w", err)
	}

	conn := inner.GetConn(gid)
	if conn == nil {
		return fmt.Errorf("no voice connection for guild %s", guildID)
	}

	// Set speaking flag
	if err := conn.SetSpeaking(ctx, disgovoice.SpeakingFlagMicrophone); err != nil {
		return fmt.Errorf("failed to set speaking: %w", err)
	}
	defer func() {
		_ = conn.SetSpeaking(context.Background(), disgovoice.SpeakingFlagNone)
	}()

	udp := conn.UDP()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	reader := &frameReader{data: opusData}
	first := true
	for {
		if !first {
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		first = false

		frame, err := reader.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading opus frame: %w", err)
		}
		if _, err := udp.Write(frame); err != nil {
			return fmt.Errorf("writing opus frame: %w", err)
		}
	}

	// Send silence frames to signal end of speaking
	for i := 0; i < 5; i++ {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
		if _, err := udp.Write(disgovoice.SilenceAudioFrame); err != nil {
			break
		}
	}

	return nil
}

// disgoVoiceState builds a minimal discord.VoiceState for the disgo voice bridge.
func disgoVoiceState(guildID, userID snowflake.ID, channelID *snowflake.ID, sessionID string) disgoDiscord.VoiceState {
	return disgoDiscord.VoiceState{
		GuildID:   guildID,
		ChannelID: channelID,
		UserID:    userID,
		SessionID: sessionID,
	}
}

// frameReader reads 2-byte LE length-prefixed opus frames (the .frames format).
type frameReader struct {
	data   []byte
	offset int
}

func (r *frameReader) next() ([]byte, error) {
	if r.offset+2 > len(r.data) {
		return nil, io.EOF
	}
	frameLen := int(binary.LittleEndian.Uint16(r.data[r.offset : r.offset+2]))
	r.offset += 2
	if frameLen == 0 || r.offset+frameLen > len(r.data) {
		return nil, io.EOF
	}
	frame := r.data[r.offset : r.offset+frameLen]
	r.offset += frameLen
	return frame, nil
}
