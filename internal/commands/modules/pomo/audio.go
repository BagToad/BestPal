package pomo

import (
	"embed"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/bwmarrin/discordgo"
)

//go:embed assets/break_bell.frames
//go:embed assets/resume_chime.frames
var audioAssets embed.FS

// Sound identifiers
const (
	SoundBreakBell   = "assets/break_bell.frames"
	SoundResumeChime = "assets/resume_chime.frames"
)

// playSound plays an embedded opus frame file over a voice connection.
// Blocks until playback is complete. The voice connection must be ready.
func playSound(vc *discordgo.VoiceConnection, soundFile string) error {
	data, err := audioAssets.ReadFile(soundFile)
	if err != nil {
		return err
	}

	r := newFrameReader(data)

	if err := vc.Speaking(true); err != nil {
		return err
	}
	defer vc.Speaking(false)

	for {
		frame, err := r.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		vc.OpusSend <- frame
		time.Sleep(20 * time.Millisecond) // 20ms per opus frame
	}

	// Brief silence after playback to avoid clipping
	time.Sleep(100 * time.Millisecond)
	return nil
}

// joinVoice joins the specified voice channel. Returns the voice connection.
//
// discordgo's ChannelVoiceJoin has a known race condition: its hardcoded 10s
// timeout calls voice.Close() which can tear down a connection that was just
// established. We work around this by manually setting up the VoiceConnection
// (mirroring what ChannelVoiceJoin does internally), sending the OP4 gateway
// request, and polling for Ready with a generous timeout — crucially WITHOUT
// calling the destructive Close() on timeout.
func joinVoice(s *discordgo.Session, guildID, channelID string) (*discordgo.VoiceConnection, error) {
	// Ensure VoiceConnections map exists
	s.Lock()
	if s.VoiceConnections == nil {
		s.VoiceConnections = make(map[string]*discordgo.VoiceConnection)
	}
	s.Unlock()

	// Get or create the VoiceConnection object. We set the exported fields
	// that ChannelVoiceJoin sets. The unexported fields (session, mute, deaf)
	// are set by ChannelVoiceJoin internally — we can't set them directly,
	// but onVoiceServerUpdate only needs session (set via ChannelVoiceJoin's
	// first call) and the token/endpoint (set by onVoiceServerUpdate itself).
	// So we use ChannelVoiceJoin but DON'T treat its timeout as fatal.
	vc, joinErr := s.ChannelVoiceJoin(guildID, channelID, false, true)
	if joinErr == nil {
		return vc, nil
	}

	// ChannelVoiceJoin timed out and called Close(). The VC object is still
	// in VoiceConnections with session/guildID/channelID set but ws/udp torn
	// down. Send a fresh OP4 to trigger onVoiceServerUpdate → open() again.
	if retryErr := s.ChannelVoiceJoinManual(guildID, channelID, false, true); retryErr != nil {
		return nil, fmt.Errorf("voice join manual retry: %w", retryErr)
	}

	// Poll for Ready with 30s timeout (no destructive Close on failure)
	for i := 0; i < 60; i++ {
		time.Sleep(500 * time.Millisecond)
		s.RLock()
		vc = s.VoiceConnections[guildID]
		s.RUnlock()
		if vc != nil {
			vc.RLock()
			ready := vc.Ready
			vc.RUnlock()
			if ready {
				return vc, nil
			}
		}
	}

	return nil, fmt.Errorf("voice connection not ready after 30s: %w", joinErr)
}

// leaveVoice disconnects from the voice channel.
func leaveVoice(vc *discordgo.VoiceConnection) {
	if vc != nil {
		vc.Disconnect()
	}
}

// frameReader reads length-prefixed opus frames from a byte slice.
// Format: repeating [2-byte LE uint16 length][N bytes opus data]
type frameReader struct {
	data   []byte
	offset int
}

func newFrameReader(data []byte) *frameReader {
	return &frameReader{data: data}
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
