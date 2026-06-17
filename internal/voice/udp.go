package voice

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// ipDiscoveryTimeout bounds how long we wait for the voice server to echo our
// public address back during IP discovery.
const ipDiscoveryTimeout = 10 * time.Second

// audioWriter owns the UDP socket to the voice server and turns Opus frames into
// encrypted RTP packets.
type audioWriter struct {
	conn      *net.UDPConn
	ssrc      uint32
	enc       *encrypter
	sequence  uint16
	timestamp uint32
}

// dialAudio opens the UDP socket to the voice server and performs IP discovery,
// returning the writer plus our public address/port (which must be reported to
// the gateway via Select Protocol).
func dialAudio(address string, port int, ssrc uint32) (*audioWriter, string, int, error) {
	addr := &net.UDPAddr{IP: net.ParseIP(address), Port: port}
	if addr.IP == nil {
		// address may be a hostname; resolve it.
		resolved, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", address, port))
		if err != nil {
			return nil, "", 0, fmt.Errorf("voice: resolve UDP address %q: %w", address, err)
		}
		addr = resolved
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, "", 0, fmt.Errorf("voice: dial UDP: %w", err)
	}

	ip, ourPort, err := discoverIP(conn, ssrc)
	if err != nil {
		conn.Close()
		return nil, "", 0, err
	}

	return &audioWriter{conn: conn, ssrc: ssrc}, ip, ourPort, nil
}

// setEncrypter installs the AEAD encrypter once the gateway has provided the
// secret key. Audio cannot be sent before this is set.
func (w *audioWriter) setEncrypter(enc *encrypter) {
	w.enc = enc
}

// writeFrame sends one Opus frame, advancing the RTP sequence number by one and
// the timestamp by the frame's per-channel sample count.
func (w *audioWriter) writeFrame(opus []byte, samples int) error {
	if w.enc == nil {
		return fmt.Errorf("voice: cannot send audio before session description")
	}

	var header [rtpHeaderSize]byte
	header[0] = 0x80 // RTP version 2
	header[1] = 0x78 // payload type 120 (Opus)
	binary.BigEndian.PutUint16(header[2:4], w.sequence)
	binary.BigEndian.PutUint32(header[4:8], w.timestamp)
	binary.BigEndian.PutUint32(header[8:12], w.ssrc)

	packet := w.enc.encrypt(header, opus)
	if _, err := w.conn.Write(packet); err != nil {
		return fmt.Errorf("voice: write audio packet: %w", err)
	}

	w.sequence++
	w.timestamp += uint32(samples)
	return nil
}

func (w *audioWriter) close() error {
	if w.conn == nil {
		return nil
	}
	return w.conn.Close()
}

// discoverIP performs Discord's UDP IP discovery: it sends a 74-byte request and
// reads the server's reply, which contains the public IP and port the voice
// server sees for this socket. Both are needed for the Select Protocol payload.
//
// See https://discord.com/developers/docs/topics/voice-connections#ip-discovery
func discoverIP(conn *net.UDPConn, ssrc uint32) (string, int, error) {
	const (
		discoveryLen  = 74
		requestType   = 0x1
		responseType  = 0x2
		messageLength = 70
	)

	req := make([]byte, discoveryLen)
	binary.BigEndian.PutUint16(req[0:2], requestType)
	binary.BigEndian.PutUint16(req[2:4], messageLength)
	binary.BigEndian.PutUint32(req[4:8], ssrc)

	if err := conn.SetDeadline(time.Now().Add(ipDiscoveryTimeout)); err != nil {
		return "", 0, fmt.Errorf("voice: set discovery deadline: %w", err)
	}
	defer conn.SetDeadline(time.Time{})

	if _, err := conn.Write(req); err != nil {
		return "", 0, fmt.Errorf("voice: send IP discovery: %w", err)
	}

	resp := make([]byte, discoveryLen)
	n, err := conn.Read(resp)
	if err != nil {
		return "", 0, fmt.Errorf("voice: read IP discovery: %w", err)
	}
	if n < discoveryLen {
		return "", 0, fmt.Errorf("voice: short IP discovery response (%d bytes)", n)
	}
	if binary.BigEndian.Uint16(resp[0:2]) != responseType {
		return "", 0, fmt.Errorf("voice: unexpected IP discovery response type")
	}

	// The IP is a null-terminated ASCII string in bytes [8:72]; the port is the
	// big-endian uint16 in the final two bytes.
	ipBytes := resp[8:72]
	if i := bytes.IndexByte(ipBytes, 0); i >= 0 {
		ipBytes = ipBytes[:i]
	}
	ip := string(ipBytes)
	port := int(binary.BigEndian.Uint16(resp[72:74]))
	if ip == "" {
		return "", 0, fmt.Errorf("voice: IP discovery returned empty address")
	}
	return ip, port, nil
}
