package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gorilla/websocket"
)

// Voice gateway opcodes (version 8).
// https://discord.com/developers/docs/topics/opcodes-and-status-codes#voice
const (
	opIdentify           = 0
	opSelectProtocol     = 1
	opReady              = 2
	opHeartbeat          = 3
	opSessionDescription = 4
	opSpeaking           = 5
	opHeartbeatACK       = 6
	opHello              = 8
)

// speakingMicrophone is the Speaking bitmask for normal voice transmission.
const speakingMicrophone = 1

// gatewayMessage is the envelope every voice gateway payload is wrapped in. The
// top-level sequence field ("s") was added in gateway v8 and must be echoed back
// in heartbeats as seq_ack so the server can replay missed messages on resume.
type gatewayMessage struct {
	Op  int             `json:"op"`
	D   json.RawMessage `json:"d,omitempty"`
	Seq int             `json:"s,omitempty"`
}

type helloData struct {
	HeartbeatInterval float64 `json:"heartbeat_interval"`
}

type readyData struct {
	SSRC  uint32   `json:"ssrc"`
	IP    string   `json:"ip"`
	Port  int      `json:"port"`
	Modes []string `json:"modes"`
}

// sessionDescriptionData carries the negotiated mode and secret key. Note that
// secret_key arrives as a JSON array of integers, so it must be decoded as []int
// (encoding/json expects base64 for a []byte target and would fail).
type sessionDescriptionData struct {
	Mode      string `json:"mode"`
	SecretKey []int  `json:"secret_key"`
}

type identifyData struct {
	ServerID               string `json:"server_id"`
	UserID                 string `json:"user_id"`
	SessionID              string `json:"session_id"`
	Token                  string `json:"token"`
	MaxDaveProtocolVersion int    `json:"max_dave_protocol_version"`
}

type selectProtocolData struct {
	Protocol string                `json:"protocol"`
	Data     selectProtocolUDPData `json:"data"`
}

type selectProtocolUDPData struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Mode    string `json:"mode"`
}

type speakingData struct {
	Speaking int    `json:"speaking"`
	Delay    int    `json:"delay"`
	SSRC     uint32 `json:"ssrc"`
}

type heartbeatData struct {
	T      int64 `json:"t"`
	SeqAck int   `json:"seq_ack"`
}

// gateway is a single-shot client for the Discord voice gateway. It is not
// resilient to disconnects by design: TTS playback lasts seconds, so on any
// failure the whole session is torn down and retried by the caller.
type gateway struct {
	ws     *websocket.Conn
	logger *log.Logger

	writeMu sync.Mutex

	heartbeatInterval time.Duration
	seqMu             sync.Mutex
	lastSeq           int

	closeOnce sync.Once
	done      chan struct{}
}

// handshakeTimeout bounds each blocking read during the handshake so a silent
// server cannot hang the caller.
const handshakeTimeout = 20 * time.Second

// setReadDeadline applies a read deadline to the underlying socket. A zero time
// clears it (used for the steady-state read loop).
func (g *gateway) setReadDeadline(t time.Time) {
	_ = g.ws.SetReadDeadline(t)
}

// connectGateway dials the voice websocket and reads the initial Hello, returning
// a gateway with its heartbeat interval populated. The caller should then start
// the heartbeat and proceed through the handshake.
func connectGateway(ctx context.Context, endpoint string, logger *log.Logger) (*gateway, error) {
	url := buildGatewayURL(endpoint)

	dialer := websocket.Dialer{HandshakeTimeout: 20 * time.Second}
	ws, resp, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("voice: dial gateway %q: %w", url, err)
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}

	g := &gateway{
		ws:     ws,
		logger: logger,
		done:   make(chan struct{}),
	}

	g.setReadDeadline(time.Now().Add(handshakeTimeout))
	raw, err := g.readUntil(opHello)
	if err != nil {
		g.close()
		return nil, err
	}
	var hello helloData
	if err := json.Unmarshal(raw, &hello); err != nil {
		g.close()
		return nil, fmt.Errorf("voice: decode hello: %w", err)
	}
	g.heartbeatInterval = time.Duration(hello.HeartbeatInterval) * time.Millisecond
	if g.heartbeatInterval <= 0 {
		// Guard against a missing/zero interval so the ticker never panics.
		g.heartbeatInterval = 15 * time.Second
	}
	return g, nil
}

// buildGatewayURL turns a raw Discord voice endpoint into a v8 websocket URL.
// Endpoints arrive without a scheme and may carry a legacy ":80" suffix.
func buildGatewayURL(endpoint string) string {
	endpoint = strings.TrimPrefix(endpoint, "wss://")
	endpoint = strings.TrimPrefix(endpoint, "ws://")
	endpoint = strings.TrimSuffix(endpoint, ":80")
	return "wss://" + endpoint + "?v=8"
}

// send writes a payload with the given opcode, serialized against concurrent
// writers (the heartbeat loop).
func (g *gateway) send(op int, d any) error {
	var raw json.RawMessage
	if d != nil {
		b, err := json.Marshal(d)
		if err != nil {
			return fmt.Errorf("voice: marshal op %d: %w", op, err)
		}
		raw = b
	}
	msg := gatewayMessage{Op: op, D: raw}

	g.writeMu.Lock()
	defer g.writeMu.Unlock()
	if err := g.ws.WriteJSON(msg); err != nil {
		return fmt.Errorf("voice: write op %d: %w", op, err)
	}
	return nil
}

// readMessage reads and decodes the next gateway message, tracking the v8
// sequence number.
func (g *gateway) readMessage() (gatewayMessage, error) {
	_, data, err := g.ws.ReadMessage()
	if err != nil {
		return gatewayMessage{}, fmt.Errorf("voice: read gateway: %w", err)
	}
	var msg gatewayMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return gatewayMessage{}, fmt.Errorf("voice: decode gateway message: %w", err)
	}
	if msg.Seq != 0 {
		g.seqMu.Lock()
		g.lastSeq = msg.Seq
		g.seqMu.Unlock()
	}
	return msg, nil
}

// readUntil reads messages until one with the wanted opcode arrives, returning
// its data. Other opcodes (e.g. heartbeat ACKs) are skipped.
func (g *gateway) readUntil(op int) (json.RawMessage, error) {
	for {
		msg, err := g.readMessage()
		if err != nil {
			return nil, err
		}
		if msg.Op == op {
			return msg.D, nil
		}
	}
}

// identify sends the Identify payload. max_dave_protocol_version is 0 to opt out
// of DAVE/MLS end-to-end encryption and keep the connection on the plain path.
func (g *gateway) identify(guildID, userID, sessionID, token string) error {
	return g.send(opIdentify, identifyData{
		ServerID:               guildID,
		UserID:                 userID,
		SessionID:              sessionID,
		Token:                  token,
		MaxDaveProtocolVersion: 0,
	})
}

// awaitReady waits for the Ready payload describing the SSRC and UDP endpoint.
func (g *gateway) awaitReady() (readyData, error) {
	raw, err := g.readUntil(opReady)
	if err != nil {
		return readyData{}, err
	}
	var ready readyData
	if err := json.Unmarshal(raw, &ready); err != nil {
		return readyData{}, fmt.Errorf("voice: decode ready: %w", err)
	}
	return ready, nil
}

// selectProtocol reports our discovered address/port and chosen encryption mode.
func (g *gateway) selectProtocol(address string, port int, mode string) error {
	return g.send(opSelectProtocol, selectProtocolData{
		Protocol: "udp",
		Data: selectProtocolUDPData{
			Address: address,
			Port:    port,
			Mode:    mode,
		},
	})
}

// awaitSessionDescription waits for the secret key and confirmed encryption mode.
func (g *gateway) awaitSessionDescription() (string, []byte, error) {
	raw, err := g.readUntil(opSessionDescription)
	if err != nil {
		return "", nil, err
	}
	var sd sessionDescriptionData
	if err := json.Unmarshal(raw, &sd); err != nil {
		return "", nil, fmt.Errorf("voice: decode session description: %w", err)
	}
	key := make([]byte, len(sd.SecretKey))
	for i, v := range sd.SecretKey {
		key[i] = byte(v)
	}
	return sd.Mode, key, nil
}

// setSpeaking announces whether we are transmitting audio. Discord requires a
// Speaking frame with the microphone bit set before the first audio packet.
func (g *gateway) setSpeaking(speaking bool, ssrc uint32) error {
	var flag int
	if speaking {
		flag = speakingMicrophone
	}
	return g.send(opSpeaking, speakingData{Speaking: flag, Delay: 0, SSRC: ssrc})
}

// startHeartbeat launches the heartbeat loop and a reader that drains steady-state
// messages so heartbeat ACKs and disconnects are observed. Call after the
// handshake completes.
func (g *gateway) startHeartbeat() {
	go g.heartbeatLoop()
	go g.readLoop()
}

func (g *gateway) heartbeatLoop() {
	ticker := time.NewTicker(g.heartbeatInterval)
	defer ticker.Stop()

	// Send one heartbeat immediately so playback that outlasts a single interval
	// stays alive from the start.
	sendBeat := func() bool {
		g.seqMu.Lock()
		seqAck := g.lastSeq
		g.seqMu.Unlock()
		payload := heartbeatData{T: time.Now().UnixMilli(), SeqAck: seqAck}
		if err := g.send(opHeartbeat, payload); err != nil {
			if g.logger != nil {
				g.logger.Debugf("voice: heartbeat failed: %v", err)
			}
			return false
		}
		return true
	}

	if !sendBeat() {
		return
	}
	for {
		select {
		case <-g.done:
			return
		case <-ticker.C:
			if !sendBeat() {
				return
			}
		}
	}
}

// readLoop drains messages after the handshake to keep sequence tracking current
// and detect server-side disconnects. It exits on any read error.
func (g *gateway) readLoop() {
	for {
		select {
		case <-g.done:
			return
		default:
		}
		if _, err := g.readMessage(); err != nil {
			return
		}
	}
}

func (g *gateway) close() {
	g.closeOnce.Do(func() {
		close(g.done)
		g.writeMu.Lock()
		_ = g.ws.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second))
		g.writeMu.Unlock()
		g.ws.Close()
	})
}
