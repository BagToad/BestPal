package pomo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	ffmpegVersion = "7.1"
	ffmpegBaseURL = "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/"
)

var (
	ffmpegPath string
	ffmpegOnce sync.Once
	ffmpegErr  error
)

// ensureFFmpeg downloads a static ffmpeg binary if not already cached.
func ensureFFmpeg() (string, error) {
	ffmpegOnce.Do(func() {
		ffmpegPath, ffmpegErr = resolveFFmpeg()
	})
	return ffmpegPath, ffmpegErr
}

func resolveFFmpeg() (string, error) {
	// Check if ffmpeg is already on PATH
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p, nil
	}

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = "/tmp"
	}
	binDir := filepath.Join(cacheDir, "gamerpal", "bin")
	binPath := filepath.Join(binDir, "ffmpeg")

	if _, err := os.Stat(binPath); err == nil {
		return binPath, nil
	}

	// Download static ffmpeg
	url, err := ffmpegDownloadURL()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}

	if err := downloadAndExtractFFmpeg(url, binPath); err != nil {
		os.Remove(binPath)
		return "", fmt.Errorf("download ffmpeg: %w", err)
	}

	return binPath, nil
}

func ffmpegDownloadURL() (string, error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	switch {
	case goos == "linux" && goarch == "amd64":
		return ffmpegBaseURL + "ffmpeg-master-latest-linux64-gpl.tar.xz", nil
	case goos == "linux" && goarch == "arm64":
		return ffmpegBaseURL + "ffmpeg-master-latest-linuxarm64-gpl.tar.xz", nil
	case goos == "darwin":
		// BtbN doesn't provide macOS builds; use evermeet.cx
		return "https://evermeet.cx/ffmpeg/getrelease/ffmpeg/zip", nil
	default:
		return "", fmt.Errorf("unsupported platform: %s/%s — install ffmpeg manually", goos, goarch)
	}
}

func downloadAndExtractFFmpeg(url, destPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if strings.HasSuffix(url, ".tar.xz") {
		return extractFromTarXz(data, destPath)
	}
	if strings.HasSuffix(url, "/zip") || strings.HasSuffix(url, ".zip") {
		return extractFromZip(data, destPath)
	}
	return fmt.Errorf("unknown archive format: %s", url)
}

func extractFromTarXz(data []byte, destPath string) error {
	// Decompress xz → tar using xz command (available on Linux)
	cmd := exec.Command("xz", "-d", "-c")
	cmd.Stdin = bytes.NewReader(data)
	var tarData bytes.Buffer
	cmd.Stdout = &tarData
	if err := cmd.Run(); err != nil {
		// Fallback: try gzip in case the format changed
		return extractFromTarGz(data, destPath)
	}
	return extractFFmpegFromTar(&tarData, destPath)
}

func extractFromTarGz(data []byte, destPath string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()
	return extractFFmpegFromTar(gz, destPath)
}

func extractFFmpegFromTar(r io.Reader, destPath string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("ffmpeg binary not found in archive")
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) == "ffmpeg" && hdr.Typeflag == tar.TypeReg {
			f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			f.Close()
			return err
		}
	}
}

func extractFromZip(data []byte, destPath string) error {
	// macOS zip is just the binary
	return os.WriteFile(destPath, data, 0o755)
}

// convertToOpusFrames converts audio data to 2-byte LE length-prefixed opus
// frames using ffmpeg. Downloads a static ffmpeg binary on first use if needed.
func convertToOpusFrames(input []byte, _ string) ([]byte, error) {
	ffmpeg, err := ensureFFmpeg()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not available: %w", err)
	}

	cmd := exec.Command(ffmpeg,
		"-i", "pipe:0",
		"-c:a", "libopus",
		"-b:a", "64k",
		"-ar", "48000",
		"-ac", "2",
		"-frame_duration", "20",
		"-vn",
		"-f", "ogg",
		"pipe:1",
	)
	cmd.Stdin = bytes.NewReader(input)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, stderr.String())
	}

	return extractOpusFromOgg(stdout.Bytes())
}

// extractOpusFromOgg extracts opus frames from an OGG container and returns
// them as 2-byte LE length-prefixed frames.
func extractOpusFromOgg(oggData []byte) ([]byte, error) {
	r := bytes.NewReader(oggData)
	var frames bytes.Buffer

	for {
		frame, err := readOggOpusFrame(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(frame) == 0 {
			continue
		}

		var lenBuf [2]byte
		binary.LittleEndian.PutUint16(lenBuf[:], uint16(len(frame)))
		frames.Write(lenBuf[:])
		frames.Write(frame)
	}

	if frames.Len() == 0 {
		return nil, fmt.Errorf("no opus frames found in audio")
	}
	return frames.Bytes(), nil
}

// readOggOpusFrame reads the next OGG page and extracts the opus data.
func readOggOpusFrame(r io.Reader) ([]byte, error) {
	var header [27]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}

	if string(header[0:4]) != "OggS" {
		return nil, fmt.Errorf("invalid OGG page magic")
	}

	numSegments := int(header[26])
	segmentTable := make([]byte, numSegments)
	if _, err := io.ReadFull(r, segmentTable); err != nil {
		return nil, err
	}

	totalSize := 0
	for _, s := range segmentTable {
		totalSize += int(s)
	}

	data := make([]byte, totalSize)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}

	// Skip OGG header pages (OpusHead, OpusTags)
	if len(data) >= 4 && string(data[0:4]) == "Opus" {
		return nil, nil
	}

	return data, nil
}

