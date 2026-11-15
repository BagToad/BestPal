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

const (
	maxTranscriptUploadBytes  = 9 << 20
	unavailableTranscriptType = "application/x-bestpal-transcript-unavailable"
)

func squadTranscript(sq database.Squad, messages []*discordgo.Message, generatedAt time.Time) (database.SquadDeletionTranscript, error) {
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
		return database.SquadDeletionTranscript{Name: name, ContentType: "text/plain; charset=utf-8", Data: bytes.Clone(data)}, nil
	}

	var compressed bytes.Buffer
	zw := zip.NewWriter(&compressed)
	w, err := zw.Create(name)
	if err != nil {
		return database.SquadDeletionTranscript{}, err
	}
	if _, err := w.Write(data); err != nil {
		return database.SquadDeletionTranscript{}, err
	}
	if err := zw.Close(); err != nil {
		return database.SquadDeletionTranscript{}, err
	}
	if compressed.Len() > maxTranscriptUploadBytes {
		return database.SquadDeletionTranscript{}, fmt.Errorf("transcript is too large to upload (%d MiB compressed)", compressed.Len()>>20)
	}
	return database.SquadDeletionTranscript{
		Name:        fmt.Sprintf("squad-%d-transcript.zip", sq.ID),
		ContentType: "application/zip",
		Data:        bytes.Clone(compressed.Bytes()),
	}, nil
}

func unavailableTranscript(reason string) database.SquadDeletionTranscript {
	return database.SquadDeletionTranscript{Name: "unavailable", ContentType: unavailableTranscriptType, Data: []byte(reason)}
}

func transcriptFile(transcript database.SquadDeletionTranscript) *discordgo.File {
	if transcript.Name == "" || transcript.ContentType == unavailableTranscriptType {
		return nil
	}
	return &discordgo.File{Name: transcript.Name, ContentType: transcript.ContentType, Reader: bytes.NewReader(transcript.Data)}
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
	fmt.Fprintf(out, "\n[%s] %s (%s) · message %s · type %d\n", msg.Timestamp.UTC().Format(time.RFC3339), author, authorID, msg.ID, msg.Type)
	if msg.EditedTimestamp != nil {
		fmt.Fprintf(out, "  Edited: %s\n", msg.EditedTimestamp.UTC().Format(time.RFC3339))
	}
	if msg.MessageReference != nil && msg.MessageReference.MessageID != "" {
		fmt.Fprintf(out, "  Reply to: %s\n", msg.MessageReference.MessageID)
	}
	if msg.Content != "" {
		for line := range strings.SplitSeq(msg.Content, "\n") {
			fmt.Fprintf(out, "  %s\n", line)
		}
	}
	for _, attachment := range msg.Attachments {
		if attachment != nil {
			fmt.Fprintf(out, "  Attachment: %s (%s)\n", attachment.Filename, attachment.URL)
		}
	}
	for _, embed := range msg.Embeds {
		if embed == nil {
			continue
		}
		if embed.Title != "" {
			fmt.Fprintf(out, "  Embed title: %s\n", embed.Title)
		}
		if embed.Description != "" {
			fmt.Fprintf(out, "  Embed description: %s\n", embed.Description)
		}
		for _, field := range embed.Fields {
			if field != nil {
				fmt.Fprintf(out, "  Embed field: %s: %s\n", field.Name, field.Value)
			}
		}
	}
	for _, sticker := range msg.StickerItems {
		if sticker != nil {
			fmt.Fprintf(out, "  Sticker: %s (%s)\n", sticker.Name, sticker.ID)
		}
	}
}
