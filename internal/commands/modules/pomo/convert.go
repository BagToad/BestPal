package pomo

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
)

// convertToOpusFrames converts raw audio data (mp3, wav, ogg, etc.) to
// 2-byte LE length-prefixed opus frames suitable for Discord voice.
// Requires ffmpeg to be installed.
func convertToOpusFrames(input []byte) ([]byte, error) {
	cmd := exec.Command("ffmpeg",
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
// them as 2-byte LE length-prefixed frames (the .frames format).
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

		// Write 2-byte LE length prefix + frame data
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
// OGG page format: "OggS" magic, then header fields, then segment table, then data.
func readOggOpusFrame(r io.Reader) ([]byte, error) {
	// Read OGG page header (27 bytes minimum)
	var header [27]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}

	// Verify magic
	if string(header[0:4]) != "OggS" {
		return nil, fmt.Errorf("invalid OGG page magic")
	}

	numSegments := int(header[26])
	segmentTable := make([]byte, numSegments)
	if _, err := io.ReadFull(r, segmentTable); err != nil {
		return nil, err
	}

	// Calculate total data size from segment table
	totalSize := 0
	for _, s := range segmentTable {
		totalSize += int(s)
	}

	data := make([]byte, totalSize)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}

	// Skip OGG header pages (OpusHead, OpusTags) — they start with "Opus"
	if len(data) >= 4 && string(data[0:4]) == "Opus" {
		return nil, nil // skip, not audio data
	}

	return data, nil
}
