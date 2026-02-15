package voice

import (
	"context"
	"io"
	"sync"
	"time"

	disgovoice "github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
)

// StreamPlayer streams opus frames in a loop over a voice connection.
// It supports pause/resume for use during pomo phase transitions.
type StreamPlayer struct {
	mu     sync.Mutex
	mgr    *Manager
	gid    snowflake.ID
	tracks [][]byte // raw .frames data (2-byte LE prefix)

	// control channels (created fresh each Play call)
	pauseCh  chan struct{}
	resumeCh chan struct{}
	stopCh   chan struct{}
	done     chan struct{}
	running  bool
}

// NewStreamPlayer creates a player for the given guild's voice connection.
// tracks is a list of opus .frames file contents (2-byte LE length-prefixed).
func NewStreamPlayer(mgr *Manager, guildID string, tracks [][]byte) *StreamPlayer {
	gid, _ := snowflake.Parse(guildID)
	return &StreamPlayer{
		mgr:    mgr,
		gid:    gid,
		tracks: tracks,
	}
}

// Play starts streaming in a background goroutine. Loops through all tracks
// continuously until Stop is called. Safe to call only once; call Stop first
// to restart.
func (sp *StreamPlayer) Play() {
	sp.mu.Lock()
	if sp.running {
		sp.mu.Unlock()
		return
	}
	sp.pauseCh = make(chan struct{}, 1)
	sp.resumeCh = make(chan struct{}, 1)
	sp.stopCh = make(chan struct{}, 1)
	sp.done = make(chan struct{})
	sp.running = true
	sp.mu.Unlock()

	go sp.run()
}

// Pause pauses playback. The player sends silence frames then idles until
// Resume or Stop is called.
func (sp *StreamPlayer) Pause() {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if !sp.running {
		return
	}
	select {
	case sp.pauseCh <- struct{}{}:
	default:
	}
}

// Resume resumes playback after a Pause.
func (sp *StreamPlayer) Resume() {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if !sp.running {
		return
	}
	select {
	case sp.resumeCh <- struct{}{}:
	default:
	}
}

// Stop stops playback and waits for the goroutine to exit.
func (sp *StreamPlayer) Stop() {
	sp.mu.Lock()
	if !sp.running {
		sp.mu.Unlock()
		return
	}
	sp.mu.Unlock()

	select {
	case sp.stopCh <- struct{}{}:
	default:
	}
	<-sp.done

	sp.mu.Lock()
	sp.running = false
	sp.mu.Unlock()
}

func (sp *StreamPlayer) run() {
	defer close(sp.done)

	if len(sp.tracks) == 0 {
		return
	}

	// Acquire the voice connection
	sp.mgr.mu.Lock()
	inner := sp.mgr.inner
	sp.mgr.mu.Unlock()
	if inner == nil {
		return
	}
	conn := inner.GetConn(sp.gid)
	if conn == nil {
		return
	}

	ctx := context.Background()
	_ = conn.SetSpeaking(ctx, disgovoice.SpeakingFlagMicrophone)
	defer func() {
		_ = conn.SetSpeaking(context.Background(), disgovoice.SpeakingFlagNone)
	}()

	udp := conn.UDP()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	trackIdx := 0
	reader := &frameReader{data: sp.tracks[trackIdx]}

	for {
		// Check control channels before sending each frame
		select {
		case <-sp.stopCh:
			sp.sendSilence(udp, ticker)
			return
		case <-sp.pauseCh:
			// Clear speaking and wait for resume or stop
			_ = conn.SetSpeaking(ctx, disgovoice.SpeakingFlagNone)
			sp.sendSilence(udp, ticker)
			select {
			case <-sp.resumeCh:
				_ = conn.SetSpeaking(ctx, disgovoice.SpeakingFlagMicrophone)
			case <-sp.stopCh:
				return
			}
		default:
		}

		// Wait for next tick
		select {
		case <-ticker.C:
		case <-sp.stopCh:
			sp.sendSilence(udp, ticker)
			return
		case <-sp.pauseCh:
			_ = conn.SetSpeaking(ctx, disgovoice.SpeakingFlagNone)
			sp.sendSilence(udp, ticker)
			select {
			case <-sp.resumeCh:
				_ = conn.SetSpeaking(ctx, disgovoice.SpeakingFlagMicrophone)
				continue
			case <-sp.stopCh:
				return
			}
		}

		frame, err := reader.next()
		if err == io.EOF {
			// Advance to next track (loop)
			trackIdx = (trackIdx + 1) % len(sp.tracks)
			reader = &frameReader{data: sp.tracks[trackIdx]}
			frame, err = reader.next()
			if err != nil {
				return // all tracks unreadable
			}
		} else if err != nil {
			return
		}

		_, _ = udp.Write(frame)
	}
}

// sendSilence sends a few silence frames to cleanly signal end of audio.
func (sp *StreamPlayer) sendSilence(udp io.Writer, ticker *time.Ticker) {
	for i := 0; i < 5; i++ {
		<-ticker.C
		_, _ = udp.Write(disgovoice.SilenceAudioFrame)
	}
}
