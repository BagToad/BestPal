package msgsearch

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// maxUploadBytes keeps attachments under Discord's default 10 MiB bot upload limit.
const maxUploadBytes = 9 << 20

var csvHeader = []string{
	"message_id", "timestamp_utc", "edited_utc",
	"author_id", "author_username", "author_display_name", "author_is_bot",
	"channel_id", "channel_name", "parent_channel_name",
	"content", "attachments", "embeds", "stickers",
	"reply_to_message_id", "pinned", "jump_url",
}

type channelInfo struct {
	Name       string
	ParentName string
}

// channelNamer resolves a channel ID to a display name (and parent for threads).
type channelNamer func(channelID string) channelInfo

// sanitizeCell neutralizes spreadsheet formula injection (=, +, -, @, tab, CR, LF).
func sanitizeCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r', '\n':
		return "'" + s
	}
	return s
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

func messageContent(m *discordgo.Message) string {
	if m.Content != "" || len(m.MessageSnapshots) == 0 {
		return m.Content
	}
	var parts []string
	for _, snap := range m.MessageSnapshots {
		if snap.Message != nil && snap.Message.Content != "" {
			parts = append(parts, snap.Message.Content)
		}
	}
	if len(parts) == 0 {
		return "[forwarded message]"
	}
	return "[forwarded] " + strings.Join(parts, "\n")
}

func messageRow(guildID string, m *discordgo.Message, names channelNamer) []string {
	var authorID, username, display, isBot string
	if m.Author != nil {
		authorID = m.Author.ID
		username = m.Author.Username
		display = m.Author.GlobalName
		isBot = strconv.FormatBool(m.Author.Bot)
	}

	attachments := make([]string, 0, len(m.Attachments))
	for _, a := range m.Attachments {
		if a != nil {
			attachments = append(attachments, a.URL)
		}
	}
	embeds := make([]string, 0, len(m.Embeds))
	for _, e := range m.Embeds {
		if e == nil {
			continue
		}
		switch {
		case e.URL != "":
			embeds = append(embeds, e.URL)
		case e.Title != "":
			embeds = append(embeds, e.Title)
		default:
			embeds = append(embeds, "["+string(e.Type)+" embed]")
		}
	}
	stickers := make([]string, 0, len(m.StickerItems))
	for _, st := range m.StickerItems {
		if st != nil {
			stickers = append(stickers, st.Name)
		}
	}
	var replyTo string
	if m.MessageReference != nil {
		replyTo = m.MessageReference.MessageID
	}
	var edited time.Time
	if m.EditedTimestamp != nil {
		edited = *m.EditedTimestamp
	}

	ch := names(m.ChannelID)
	return []string{
		m.ID,
		formatTime(m.Timestamp),
		formatTime(edited),
		authorID,
		sanitizeCell(username),
		sanitizeCell(display),
		isBot,
		m.ChannelID,
		sanitizeCell(ch.Name),
		sanitizeCell(ch.ParentName),
		sanitizeCell(messageContent(m)),
		strings.Join(attachments, "\n"),
		sanitizeCell(strings.Join(embeds, "\n")),
		sanitizeCell(strings.Join(stickers, ", ")),
		replyTo,
		strconv.FormatBool(m.Pinned),
		fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, m.ChannelID, m.ID),
	}
}

// buildCSV renders messages as UTF-8 CSV with a BOM so Excel keeps emoji intact.
func buildCSV(guildID string, msgs []*discordgo.Message, names channelNamer) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("\ufeff")
	w := csv.NewWriter(&buf)
	if err := w.Write(csvHeader); err != nil {
		return nil, err
	}
	for _, m := range msgs {
		if err := w.Write(messageRow(guildID, m, names)); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// packageExport returns the file to upload, zipping it when the CSV is too large.
func packageExport(csvName string, data []byte) (*discordgo.File, error) {
	if len(data) <= maxUploadBytes {
		return &discordgo.File{Name: csvName, ContentType: "text/csv", Reader: bytes.NewReader(data)}, nil
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create(csvName)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if buf.Len() > maxUploadBytes {
		return nil, fmt.Errorf("export is too large to upload (%d MB zipped); narrow the search or use a smaller page range", buf.Len()>>20)
	}
	return &discordgo.File{
		Name:        strings.TrimSuffix(csvName, ".csv") + ".zip",
		ContentType: "application/zip",
		Reader:      bytes.NewReader(buf.Bytes()),
	}, nil
}
