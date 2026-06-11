package scamguard

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/corona10/goimagehash"

	// Image decoders. pHash needs a decoded image; register the formats Discord
	// commonly delivers. avif/heic are unsupported and skipped at decode time.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

// computeHash decodes raw image bytes and returns their perceptual hash.
func computeHash(data []byte) (*goimagehash.ImageHash, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	h, err := goimagehash.PerceptionHash(img)
	if err != nil {
		return nil, fmt.Errorf("perception hash: %w", err)
	}
	return h, nil
}

// parseHash parses a goimagehash string form (e.g. "p:ff00...").
func parseHash(s string) (*goimagehash.ImageHash, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty hash")
	}
	return goimagehash.ImageHashFromString(s)
}

// hashString returns the canonical string form of a hash.
func hashString(h *goimagehash.ImageHash) string {
	return h.ToString()
}

// isImageAttachment reports whether the given attachment is an image (by
// content-type, falling back to extension).
func isImageAttachment(a *discordgo.MessageAttachment) bool {
	if strings.HasPrefix(strings.ToLower(a.ContentType), "image/") {
		return true
	}
	lower := strings.ToLower(a.Filename)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// defaultFetchImage performs a single GET with a short timeout, validates that
// the response looks like an image, and reads at most maxBytes.
func defaultFetchImage(url string, maxBytes int) ([]byte, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if ct != "" && !strings.HasPrefix(ct, "image/") {
		return nil, fmt.Errorf("not an image: %s", ct)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBytes {
		return nil, fmt.Errorf("image exceeds %d bytes", maxBytes)
	}
	return body, nil
}
