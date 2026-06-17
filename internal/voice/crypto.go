package voice

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// rtpHeaderSize is the size of the (unencrypted) RTP header that prefixes every
// voice packet.
const rtpHeaderSize = 12

// Encryption mode identifiers as negotiated with the Discord voice gateway. The
// "_rtpsize" suffix means the 12-byte RTP header is authenticated (as AEAD
// associated data) but not encrypted, and a 4-byte unencrypted nonce is
// appended to the packet.
//
// See https://discord.com/developers/docs/topics/voice-connections#transport-encryption-and-sending-voice
const (
	encryptionAEADAES256GCM = "aead_aes256_gcm_rtpsize"
	encryptionAEADXChaCha20 = "aead_xchacha20_poly1305_rtpsize"
)

// supportedEncryptionModes lists the modes we can speak, most preferred first.
// AES256-GCM is preferred (hardware accelerated on modern CPUs); XChaCha20 is
// the mode Discord guarantees every voice server supports.
var supportedEncryptionModes = []string{
	encryptionAEADAES256GCM,
	encryptionAEADXChaCha20,
}

// chooseEncryptionMode returns the most preferred mode that the voice server
// offered, or an error if none of ours are supported.
func chooseEncryptionMode(offered []string) (string, error) {
	for _, preferred := range supportedEncryptionModes {
		for _, m := range offered {
			if m == preferred {
				return preferred, nil
			}
		}
	}
	return "", fmt.Errorf("voice: no supported encryption mode offered by server (got %v)", offered)
}

// encrypter seals an Opus payload into a complete RTP packet ready to be written
// to the voice UDP socket.
type encrypter struct {
	cipher cipher.AEAD
	nonce  []byte
	seq    uint32
	buf    []byte
}

// newEncrypter builds an encrypter for the negotiated mode and 32-byte secret
// key supplied by the voice gateway's Session Description.
func newEncrypter(mode string, secretKey []byte) (*encrypter, error) {
	var aead cipher.AEAD
	switch mode {
	case encryptionAEADAES256GCM:
		block, err := aes.NewCipher(secretKey)
		if err != nil {
			return nil, fmt.Errorf("voice: create AES cipher: %w", err)
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("voice: create GCM cipher: %w", err)
		}
		aead = gcm
	case encryptionAEADXChaCha20:
		c, err := chacha20poly1305.NewX(secretKey)
		if err != nil {
			return nil, fmt.Errorf("voice: create XChaCha20-Poly1305 cipher: %w", err)
		}
		aead = c
	default:
		return nil, fmt.Errorf("voice: unknown encryption mode %q", mode)
	}

	maxPacket := rtpHeaderSize + maxOpusFrameSize + aead.Overhead() + 4
	return &encrypter{
		cipher: aead,
		nonce:  make([]byte, aead.NonceSize()),
		buf:    make([]byte, rtpHeaderSize, maxPacket),
	}, nil
}

// encrypt seals data (an Opus frame) under the given RTP header and returns the
// full packet: [12-byte RTP header][ciphertext+tag][4-byte little-endian nonce].
//
// The nonce is a monotonically increasing 32-bit counter placed at the start of
// the AEAD nonce (the remaining bytes stay zero) and also appended, unencrypted,
// to the tail of the packet so the receiver can reconstruct it. The RTP header
// is authenticated as associated data.
//
// The returned slice aliases the encrypter's internal buffer and is only valid
// until the next call to encrypt.
func (e *encrypter) encrypt(header [rtpHeaderSize]byte, data []byte) []byte {
	e.buf = e.buf[:rtpHeaderSize]
	copy(e.buf, header[:])

	binary.LittleEndian.PutUint32(e.nonce, e.seq)
	e.seq++

	e.buf = e.cipher.Seal(e.buf, e.nonce, data, header[:])
	e.buf = append(e.buf, e.nonce[:4]...)
	return e.buf
}
