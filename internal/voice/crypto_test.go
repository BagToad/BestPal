package voice

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestEncrypterRoundTrip(t *testing.T) {
	modes := []string{encryptionAEADAES256GCM, encryptionAEADXChaCha20}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			key := make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				t.Fatalf("rand: %v", err)
			}

			enc, err := newEncrypter(mode, key)
			if err != nil {
				t.Fatalf("newEncrypter: %v", err)
			}

			var header [rtpHeaderSize]byte
			header[0] = 0x80
			header[1] = 0x78
			header[8] = 0xDE
			header[9] = 0xAD
			plaintext := []byte("the quick brown fox jumps over the lazy dog")

			packet := enc.encrypt(header, plaintext)

			// Packet layout: [12B header][ciphertext+tag][4B nonce].
			if !bytes.Equal(packet[:rtpHeaderSize], header[:]) {
				t.Fatalf("header not preserved at front of packet")
			}
			overhead := enc.cipher.Overhead()
			wantLen := rtpHeaderSize + len(plaintext) + overhead + 4
			if len(packet) != wantLen {
				t.Fatalf("packet length = %d, want %d", len(packet), wantLen)
			}

			// Reconstruct the nonce from the 4-byte tail and decrypt.
			nonceTail := packet[len(packet)-4:]
			ciphertext := packet[rtpHeaderSize : len(packet)-4]
			nonce := make([]byte, enc.cipher.NonceSize())
			copy(nonce, nonceTail)

			got, err := enc.cipher.Open(nil, nonce, ciphertext, header[:])
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if !bytes.Equal(got, plaintext) {
				t.Fatalf("decrypted = %q, want %q", got, plaintext)
			}
		})
	}
}

func TestEncrypterNonceIncrements(t *testing.T) {
	key := make([]byte, 32)
	enc, err := newEncrypter(encryptionAEADXChaCha20, key)
	if err != nil {
		t.Fatalf("newEncrypter: %v", err)
	}
	var header [rtpHeaderSize]byte
	p1 := append([]byte(nil), enc.encrypt(header, []byte("a"))...)
	p2 := append([]byte(nil), enc.encrypt(header, []byte("a"))...)

	n1 := p1[len(p1)-4:]
	n2 := p2[len(p2)-4:]
	if bytes.Equal(n1, n2) {
		t.Fatalf("nonce did not change between packets: %x", n1)
	}
}

func TestChooseEncryptionMode(t *testing.T) {
	// Prefers AES-GCM when both are offered.
	got, err := chooseEncryptionMode([]string{encryptionAEADXChaCha20, encryptionAEADAES256GCM})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != encryptionAEADAES256GCM {
		t.Fatalf("got %q, want %q", got, encryptionAEADAES256GCM)
	}

	// Falls back to XChaCha20 when GCM is absent.
	got, err = chooseEncryptionMode([]string{"some_future_mode", encryptionAEADXChaCha20})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != encryptionAEADXChaCha20 {
		t.Fatalf("got %q, want %q", got, encryptionAEADXChaCha20)
	}

	// Errors when nothing is supported.
	if _, err := chooseEncryptionMode([]string{"xsalsa20_poly1305"}); err == nil {
		t.Fatalf("expected error for unsupported modes")
	}
}
