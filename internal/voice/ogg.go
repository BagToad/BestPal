package voice

import (
	"bytes"
	"fmt"
)

// oggCapture is the 4-byte capture pattern that begins every OGG page.
var oggCapture = []byte("OggS")

// opusHeadMagic and opusTagsMagic identify the two mandatory Opus header packets
// that precede the audio data in an OGG/Opus stream. They carry no audio and are
// skipped during demuxing.
var (
	opusHeadMagic = []byte("OpusHead")
	opusTagsMagic = []byte("OpusTags")
)

// DemuxOggOpus extracts the raw Opus audio packets from an in-memory OGG/Opus
// stream (such as the OGG_OPUS output of a TTS engine). The two Opus header
// packets (OpusHead, OpusTags) are dropped; the remaining packets are returned
// in order, ready to be sent to a voice connection.
//
// It implements just enough of the OGG container (RFC 3533) to reassemble
// packets across segment and page boundaries. Page CRCs are not verified.
func DemuxOggOpus(data []byte) ([][]byte, error) {
	// Skip any bytes before the first page.
	start := bytes.Index(data, oggCapture)
	if start < 0 {
		return nil, fmt.Errorf("voice: not an OGG stream (no OggS capture pattern)")
	}

	var (
		packets [][]byte
		current []byte
		pos     = start
	)

	for pos < len(data) {
		if !bytes.Equal(data[pos:min(pos+4, len(data))], oggCapture) {
			return nil, fmt.Errorf("voice: malformed OGG: expected page at offset %d", pos)
		}
		// Header is 27 bytes plus the segment table.
		if pos+27 > len(data) {
			return nil, fmt.Errorf("voice: truncated OGG page header at offset %d", pos)
		}

		segCount := int(data[pos+26])
		tableStart := pos + 27
		dataStart := tableStart + segCount
		if dataStart > len(data) {
			return nil, fmt.Errorf("voice: truncated OGG segment table at offset %d", pos)
		}

		segTable := data[tableStart:dataStart]
		// Sum the lacing values to find where the page body ends.
		bodyLen := 0
		for _, l := range segTable {
			bodyLen += int(l)
		}
		if dataStart+bodyLen > len(data) {
			return nil, fmt.Errorf("voice: truncated OGG page body at offset %d", pos)
		}

		// Walk the segments, assembling packets. A lacing value of 255 means the
		// packet continues into the next segment (and possibly the next page); a
		// value below 255 terminates the current packet.
		body := data[dataStart : dataStart+bodyLen]
		segPos := 0
		for _, l := range segTable {
			current = append(current, body[segPos:segPos+int(l)]...)
			segPos += int(l)
			if l < 255 {
				packets = append(packets, current)
				current = nil
			}
		}

		pos = dataStart + bodyLen
	}

	// Drop the OpusHead / OpusTags header packets; keep audio only.
	audio := make([][]byte, 0, len(packets))
	for _, p := range packets {
		if bytes.HasPrefix(p, opusHeadMagic) || bytes.HasPrefix(p, opusTagsMagic) {
			continue
		}
		if len(p) == 0 {
			continue
		}
		audio = append(audio, p)
	}
	return audio, nil
}
