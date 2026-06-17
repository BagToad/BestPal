package voice

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// lacingFor returns the OGG segment-table lacing values for a single fully
// contained packet.
func lacingFor(n int) []byte {
	var t []byte
	for n >= 255 {
		t = append(t, 255)
		n -= 255
	}
	return append(t, byte(n))
}

// oggPage builds one raw OGG page. CRC is left zero; DemuxOggOpus does not verify
// it.
func oggPage(serial, seq uint32, headerType byte, segTable, body []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("OggS")
	buf.WriteByte(0) // version
	buf.WriteByte(headerType)
	var granule [8]byte
	buf.Write(granule[:])
	binary.Write(&buf, binary.LittleEndian, serial)
	binary.Write(&buf, binary.LittleEndian, seq)
	var crc [4]byte
	buf.Write(crc[:])
	buf.WriteByte(byte(len(segTable)))
	buf.Write(segTable)
	buf.Write(body)
	return buf.Bytes()
}

func TestDemuxOggOpusBasic(t *testing.T) {
	const serial = 0x1234

	head := append([]byte("OpusHead"), 0x01, 0x02)
	tags := append([]byte("OpusTags"), 0x03, 0x04)
	audio1 := append([]byte{0x08}, bytes.Repeat([]byte{0xAA}, 99)...)  // 100 bytes
	audio2 := append([]byte{0x08}, bytes.Repeat([]byte{0xBB}, 299)...) // 300 bytes, multi-segment

	var stream []byte
	stream = append(stream, oggPage(serial, 0, 0x02, lacingFor(len(head)), head)...)
	stream = append(stream, oggPage(serial, 1, 0x00, lacingFor(len(tags)), tags)...)
	// Two complete audio packets share one page.
	seg := append(lacingFor(len(audio1)), lacingFor(len(audio2))...)
	body := append(append([]byte{}, audio1...), audio2...)
	stream = append(stream, oggPage(serial, 2, 0x00, seg, body)...)

	packets, err := DemuxOggOpus(stream)
	if err != nil {
		t.Fatalf("DemuxOggOpus: %v", err)
	}
	if len(packets) != 2 {
		t.Fatalf("got %d packets, want 2", len(packets))
	}
	if !bytes.Equal(packets[0], audio1) {
		t.Fatalf("packet0 mismatch")
	}
	if !bytes.Equal(packets[1], audio2) {
		t.Fatalf("packet1 mismatch")
	}
}

func TestDemuxOggOpusCrossPage(t *testing.T) {
	const serial = 0x9999

	// A single 355-byte audio packet split across two pages: 255 bytes (a 255
	// lacing, so "continued") on page 0, and 100 bytes terminating on page 1.
	part1 := append([]byte{0x08}, bytes.Repeat([]byte{0xCC}, 254)...) // 255 bytes
	part2 := bytes.Repeat([]byte{0xDD}, 100)
	full := append(append([]byte{}, part1...), part2...)

	var stream []byte
	stream = append(stream, oggPage(serial, 0, 0x02, []byte{255}, part1)...)
	stream = append(stream, oggPage(serial, 1, 0x01, []byte{100}, part2)...)

	packets, err := DemuxOggOpus(stream)
	if err != nil {
		t.Fatalf("DemuxOggOpus: %v", err)
	}
	if len(packets) != 1 {
		t.Fatalf("got %d packets, want 1", len(packets))
	}
	if !bytes.Equal(packets[0], full) {
		t.Fatalf("reassembled packet mismatch: got %d bytes, want %d", len(packets[0]), len(full))
	}
}

func TestDemuxOggOpusRejectsNonOgg(t *testing.T) {
	if _, err := DemuxOggOpus([]byte("not an ogg stream")); err == nil {
		t.Fatalf("expected error for non-OGG input")
	}
}

func TestDemuxOggOpusRejectsTruncated(t *testing.T) {
	const serial = 0x1
	audio := append([]byte{0x08}, bytes.Repeat([]byte{0xAA}, 50)...)
	page := oggPage(serial, 0, 0x00, lacingFor(len(audio)), audio)
	// Chop off part of the body.
	if _, err := DemuxOggOpus(page[:len(page)-10]); err == nil {
		t.Fatalf("expected error for truncated page")
	}
}
