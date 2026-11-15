package squads

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"time"

	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
)

const maxTranscriptUploadBytes = 9 << 20

func squadTranscriptFile(sq database.Squad, messages []*discordgo.Message, generatedAt time.Time) (*discordgo.File, error) {
	var out bytes.Buffer
	fmt.Fprintf(&out, "Squad channel transcript: %s\n", sq.Name)
	fmt.Fprintf(&out, "Channel ID: %s\n", sq.ChannelID)
	fmt.Fprintf(&out, "Generated at: %s\n", generatedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&out, "Messages: %d\n", len(messages))
	out.WriteString(strings.Repeat("=", 80) + "\n")

	for _, msg := range messages {
		writeTranscriptMessage(&out, msg)
	}

	name := fmt.Sprintf("squad-%d-transcript.txt", sq.ID)
	data := out.Bytes()
	if len(data) <= maxTranscriptUploadBytes {
		return &discordgo.File{Name: name, ContentType: "text/plain; charset=utf-8", Reader: bytes.NewReader(data)}, nil
	}

	var compressed bytes.Buffer
	zw := zip.NewWriter(&compressed)
	w, err := zw.Create(name)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if compressed.Len() > maxTranscriptUploadBytes {
		return nil, fmt.Errorf("transcript is too large to upload (%d MiB compressed)", compressed.Len()>>20)
	}
	return &discordgo.File{
		Name:        fmt.Sprintf("squad-%d-transcript.zip", sq.ID),
		ContentType: "application/zip",
		Reader:      bytes.NewReader(compressed.Bytes()),
	}, nil
}

func writeTranscriptMessage(out *bytes.Buffer, msg *discordgo.Message) {
	if msg == nil {
		return
	}
	author := "Unknown user"
	authorID := "unknown"
	if msg.Author != nil {
		authorID = msg.Author.ID
		author = msg.Author.GlobalName
		if author == "" {
			author = msg.Author.Username
		}
	}
	fmt.Fprintf(out, "\n[%s] %s (%s) · message %s\n", msg.Timestamp.UTC().Format(time.RFC3339), author, authorID, msg.ID)
	if msg.Content != "" {
		out.WriteString(msg.Content + "\n")
	}
	for _, attachment := range msg.Attachments {
		if attachment != nil {
			fmt.Fprintf(out, "Attachment: %s (%s)\n", attachment.Filename, attachment.URL)
		}
	}
	for _, embed := range msg.Embeds {
		if embed == nil {
			continue
		}
		if embed.Title != "" {
			fmt.Fprintf(out, "Embed title: %s\n", embed.Title)
		}
		if embed.Description != "" {
			fmt.Fprintf(out, "Embed description: %s\n", embed.Description)
		}
		for _, field := range embed.Fields {
			if field != nil {
				fmt.Fprintf(out, "Embed field: %s: %s\n", field.Name, field.Value)
			}
		}
	}
}
