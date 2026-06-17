package voice

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/log"
)

// Credentials are the values the main Discord gateway provides (via Voice State
// Update and Voice Server Update) that authorize and locate a voice connection.
type Credentials struct {
	GuildID   string
	UserID    string
	SessionID string
	Token     string
	Endpoint  string
}

// Conn is an established voice connection: a completed gateway handshake plus a
// ready-to-write encrypted UDP media path.
type Conn struct {
	gw     *gateway
	audio  *audioWriter
	ssrc   uint32
	logger *log.Logger
}

// silenceFrameCount is how many silence frames to append after speech to flush
// Discord's jitter buffer so the tail of the audio is not clipped.
const silenceFrameCount = 5

// Dial performs the full voice handshake and returns a ready Conn. The provided
// context bounds the handshake; once Dial returns, playback uses its own timing.
func Dial(ctx context.Context, creds Credentials, logger *log.Logger) (*Conn, error) {
	gw, err := connectGateway(ctx, creds.Endpoint, logger)
	if err != nil {
		return nil, err
	}

	if err := gw.identify(creds.GuildID, creds.UserID, creds.SessionID, creds.Token); err != nil {
		gw.close()
		return nil, err
	}

	gw.setReadDeadline(time.Now().Add(handshakeTimeout))
	ready, err := gw.awaitReady()
	if err != nil {
		gw.close()
		return nil, err
	}

	mode, err := chooseEncryptionMode(ready.Modes)
	if err != nil {
		gw.close()
		return nil, err
	}

	audio, ourIP, ourPort, err := dialAudio(ready.IP, ready.Port, ready.SSRC)
	if err != nil {
		gw.close()
		return nil, err
	}

	if err := gw.selectProtocol(ourIP, ourPort, mode); err != nil {
		audio.close()
		gw.close()
		return nil, err
	}

	gw.setReadDeadline(time.Now().Add(handshakeTimeout))
	confirmedMode, secretKey, err := gw.awaitSessionDescription()
	if err != nil {
		audio.close()
		gw.close()
		return nil, err
	}
	if confirmedMode != "" {
		mode = confirmedMode
	}

	enc, err := newEncrypter(mode, secretKey)
	if err != nil {
		audio.close()
		gw.close()
		return nil, err
	}
	audio.setEncrypter(enc)

	// Handshake complete: drop the read deadline and start steady-state
	// heartbeat + read draining.
	gw.setReadDeadline(time.Time{})
	gw.startHeartbeat()

	return &Conn{gw: gw, audio: audio, ssrc: ready.SSRC, logger: logger}, nil
}

// Play sends a sequence of Opus frames in real time, announcing speaking state
// around the audio and flushing with silence at the end. It blocks until all
// frames are sent or the context is cancelled.
func (c *Conn) Play(ctx context.Context, frames [][]byte) error {
	if len(frames) == 0 {
		return nil
	}

	if err := c.gw.setSpeaking(true, c.ssrc); err != nil {
		return err
	}
	defer c.gw.setSpeaking(false, c.ssrc)

	start := time.Now()
	var played int64 // total samples sent, for pacing

	send := func(frame []byte) error {
		samples := OpusPacketSamples(frame)
		if err := c.audio.writeFrame(frame, samples); err != nil {
			return err
		}
		played += int64(samples)
		target := start.Add(time.Duration(played) * time.Second / sampleRate)
		if d := time.Until(target); d > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
			}
		}
		return nil
	}

	for _, frame := range frames {
		if len(frame) == 0 {
			continue
		}
		if err := send(frame); err != nil {
			return err
		}
	}

	for i := 0; i < silenceFrameCount; i++ {
		if err := send(silenceFrame); err != nil {
			return err
		}
	}
	return nil
}

// Close announces we have stopped speaking and tears down the UDP and gateway
// connections.
func (c *Conn) Close() error {
	if c == nil {
		return nil
	}
	_ = c.gw.setSpeaking(false, c.ssrc)
	c.gw.close()
	if err := c.audio.close(); err != nil {
		return fmt.Errorf("voice: close audio: %w", err)
	}
	return nil
}
