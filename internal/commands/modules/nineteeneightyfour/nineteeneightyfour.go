// Package nineteeneightyfour implements the "1984" audit module:
// it logs every message create, edit, delete, and reaction add/remove across
// every channel (except the configured 1984 log channel itself) to a single
// audit channel. Designed to never fail on long content - anything too large
// to inline is sent as a .txt file attachment.
package nineteeneightyfour

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"github.com/pmezard/go-difflib/difflib"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
)

// voiceChannelStatusUpdateRawType is the discord gateway event name for voice
// channel status updates. discordgo doesn't model this as a typed event so we
// catch it via the raw event handler.
const voiceChannelStatusUpdateRawType = "VOICE_CHANNEL_STATUS_UPDATE"

// voiceChannelStatusUpdate is the JSON payload for a VOICE_CHANNEL_STATUS_UPDATE
// gateway event. Not provided as a typed struct by discordgo v0.29.0.
type voiceChannelStatusUpdate struct {
	ID      string `json:"id"`
	GuildID string `json:"guild_id"`
	Status  string `json:"status"`
}

// Module implements the CommandModule interface but registers no slash
// commands. Its work is done entirely via Discord event handlers wired up
// from bot.go.
type Module struct {
	config *config.Config

	// dispatchLog sends a fully-built payload to the given channel. Overridable
	// in tests so handler logic can be exercised without hitting the network.
	dispatchLog func(s *discordgo.Session, channelID string, p payload) error
}

// New creates a new 1984 module instance.
func New(deps *types.Dependencies) *Module {
	m := &Module{config: deps.Config}
	m.dispatchLog = m.defaultDispatchLog
	return m
}

// Register satisfies the CommandModule interface (no commands to register).
func (m *Module) Register(_ map[string]*types.Command, deps *types.Dependencies) {
	m.config = deps.Config
	if m.dispatchLog == nil {
		m.dispatchLog = m.defaultDispatchLog
	}
}

// Service returns nil; this module has no recurring service.
func (m *Module) Service() types.ModuleService { return nil }

// ----------------------------------------------------------------------------
// Event handlers - wired up from bot.go.
// ----------------------------------------------------------------------------

// OnMessageCreate logs every newly sent message.
func (m *Module) OnMessageCreate(s *discordgo.Session, e *discordgo.MessageCreate) {
	if e == nil || e.Message == nil {
		return
	}
	if !m.shouldLog(s, e.GuildID, e.ChannelID, e.Author) {
		return
	}

	channelName, isVoiceText := m.channelInfo(s, e.ChannelID)

	header := buildHeader("📝 Message Sent", e.Author, e.ChannelID, channelName, e.ID, isVoiceText)
	body := strings.TrimSpace(e.Content)
	// A message create is worth logging if it has any user-visible payload:
	// text, attachments, embeds, stickers, polls, or interactive components.
	if body == "" && len(e.Attachments) == 0 && len(e.Embeds) == 0 &&
		len(e.StickerItems) == 0 && e.Poll == nil && len(e.Components) == 0 {
		return
	}

	parts := []string{header}
	if body != "" {
		parts = append(parts, "**Content:**", codeBlock(body))
	}
	if len(e.Attachments) > 0 {
		parts = append(parts, fmt.Sprintf("**Attachments:** %d", len(e.Attachments)))
	}
	if len(e.StickerItems) > 0 {
		parts = append(parts, fmt.Sprintf("**Stickers:** %d", len(e.StickerItems)))
	}
	if e.Poll != nil {
		parts = append(parts, "**Poll:** yes")
	}
	if len(e.Components) > 0 {
		parts = append(parts, fmt.Sprintf("**Components:** %d", len(e.Components)))
	}

	m.send(s, strings.Join(parts, "\n"), namedFile("content.txt", body))
}

// OnMessageUpdate logs message edits with a before/after view.
//
// MESSAGE_UPDATE is a partial gateway payload: unchanged fields can be
// omitted. That means we cannot trust e.Content == "" as "cleared" and we
// cannot trust e.Author == nil as "unknown". To avoid false positives:
//   - we fall back to the cached author when the payload omits it
//   - we require e.EditedTimestamp != nil, which Discord only sets on real
//     user edits (embed unfurls and other system updates leave it nil)
//   - we require a cached BeforeUpdate so before/after can actually be diffed
func (m *Module) OnMessageUpdate(s *discordgo.Session, e *discordgo.MessageUpdate) {
	if e == nil || e.Message == nil {
		return
	}
	if e.BeforeUpdate == nil {
		return
	}
	if e.EditedTimestamp == nil {
		return
	}

	author := e.Author
	if author == nil {
		author = e.BeforeUpdate.Author
	}
	if author == nil {
		return
	}
	if !m.shouldLog(s, e.GuildID, e.ChannelID, author) {
		return
	}

	before := e.BeforeUpdate.Content
	after := e.Content
	if before == after {
		return
	}

	channelName, isVoiceText := m.channelInfo(s, e.ChannelID)
	header := buildHeader("✏️ Message Edited", author, e.ChannelID, channelName, e.ID, isVoiceText)

	diff := renderUnifiedDiff(before, after)
	parts := []string{
		header,
		"**Diff:**",
		codeBlockLang("diff", diff),
	}

	m.send(s, strings.Join(parts, "\n"),
		namedFile("before.txt", before),
		namedFile("after.txt", after),
		namedFile("diff.patch", diff),
	)
}

// OnMessageDelete logs message deletions, including the deleted content when
// it was cached.
func (m *Module) OnMessageDelete(s *discordgo.Session, e *discordgo.MessageDelete) {
	if e == nil || e.Message == nil {
		return
	}

	// We may not know the author (uncached delete). Still skip if it's the
	// log channel itself.
	if !m.shouldLogChannel(e.ChannelID) {
		return
	}
	if e.GuildID == "" {
		return
	}

	var author *discordgo.User
	var content string
	if e.BeforeDelete != nil {
		author = e.BeforeDelete.Author
		content = e.BeforeDelete.Content
	}
	if author != nil && s.State != nil && s.State.User != nil && author.ID == s.State.User.ID {
		return
	}

	channelName, isVoiceText := m.channelInfo(s, e.ChannelID)
	header := buildHeader("🗑️ Message Deleted", author, e.ChannelID, channelName, e.ID, isVoiceText)

	display := content
	if e.BeforeDelete == nil {
		display = "(message was not in cache; content unavailable)"
	} else if content == "" {
		display = "(no text content)"
	}

	parts := []string{
		header,
		"**Deleted content:**",
		codeBlock(display),
	}

	m.send(s, strings.Join(parts, "\n"), namedFile("deleted.txt", display))
}

// OnMessageReactionAdd logs reactions being added.
func (m *Module) OnMessageReactionAdd(s *discordgo.Session, e *discordgo.MessageReactionAdd) {
	if e == nil || e.MessageReaction == nil {
		return
	}
	m.logReaction(s, e.MessageReaction, e.Member, "➕ Reaction Added")
}

// OnMessageReactionRemove logs reactions being removed.
func (m *Module) OnMessageReactionRemove(s *discordgo.Session, e *discordgo.MessageReactionRemove) {
	if e == nil || e.MessageReaction == nil {
		return
	}
	m.logReaction(s, e.MessageReaction, nil, "➖ Reaction Removed")
}

// OnChannelCreate logs the creation of voice/stage channels.
func (m *Module) OnChannelCreate(s *discordgo.Session, e *discordgo.ChannelCreate) {
	if e == nil || e.Channel == nil {
		return
	}
	if e.GuildID == "" || !isVoiceLike(e.Type) {
		return
	}
	if !m.shouldLogChannel(e.ID) {
		return
	}

	header := buildChannelEventHeader("🔊 Voice Channel Created", e.ID, e.Name, channelTypeName(e.Type))
	m.send(s, header)
}

// OnChannelUpdate logs voice/stage channel renames.
//
// CHANNEL_UPDATE fires for many non-rename changes (bitrate, user limit,
// permissions, region, etc). Without a cached `BeforeUpdate` we cannot tell
// whether the name changed, so we skip uncached updates rather than emit
// false-positive rename logs.
func (m *Module) OnChannelUpdate(s *discordgo.Session, e *discordgo.ChannelUpdate) {
	if e == nil || e.Channel == nil {
		return
	}
	if e.GuildID == "" || !isVoiceLike(e.Type) {
		return
	}
	if !m.shouldLogChannel(e.ID) {
		return
	}
	if e.BeforeUpdate == nil || e.BeforeUpdate.Name == e.Name {
		return
	}

	header := buildChannelEventHeader("🔊 Voice Channel Renamed", e.ID, e.Name, channelTypeName(e.Type))
	body := header + fmt.Sprintf("\n**Before:** `%s`\n**After:** `%s`", e.BeforeUpdate.Name, e.Name)
	m.send(s, body)
}

// OnRawEvent receives every gateway event so we can catch ones discordgo
// doesn't model as a typed event - currently only VOICE_CHANNEL_STATUS_UPDATE.
func (m *Module) OnRawEvent(s *discordgo.Session, e *discordgo.Event) {
	if e == nil || e.Type != voiceChannelStatusUpdateRawType {
		return
	}
	var u voiceChannelStatusUpdate
	if err := json.Unmarshal(e.RawData, &u); err != nil {
		return
	}
	if u.GuildID == "" {
		return
	}
	if !m.shouldLogChannel(u.ID) {
		return
	}

	name, _ := m.channelInfo(s, u.ID)
	if name == "" {
		name = "(unknown)"
	}
	header := buildChannelEventHeader("🔊 Voice Channel Status Updated", u.ID, name, "voice")
	display := u.Status
	if display == "" {
		display = "(cleared)"
	}
	body := header + "\n**Status:**\n" + codeBlock(display)
	m.send(s, body, namedFile("status.txt", display))
}

// ----------------------------------------------------------------------------
// Internals
// ----------------------------------------------------------------------------

func (m *Module) logReaction(s *discordgo.Session, r *discordgo.MessageReaction, member *discordgo.Member, title string) {
	if r.GuildID == "" {
		return
	}
	if !m.shouldLogChannel(r.ChannelID) {
		return
	}
	if s.State != nil && s.State.User != nil && r.UserID == s.State.User.ID {
		return
	}

	user := userFromMember(member)

	channelName, isVoiceText := m.channelInfo(s, r.ChannelID)
	header := buildHeader(title, user, r.ChannelID, channelName, r.MessageID, isVoiceText)
	if user == nil {
		header += fmt.Sprintf("\n**User ID:** `%s`", r.UserID)
	}

	emoji := r.Emoji.Name
	if r.Emoji.ID != "" {
		emoji = fmt.Sprintf("%s (`%s`)", r.Emoji.Name, r.Emoji.ID)
	}

	body := header + "\n**Emoji:** " + emoji
	m.send(s, body)
}

// shouldLog returns true if this guild message should be logged.
func (m *Module) shouldLog(s *discordgo.Session, guildID, channelID string, author *discordgo.User) bool {
	if guildID == "" {
		return false
	}
	if !m.shouldLogChannel(channelID) {
		return false
	}
	if author != nil && s.State != nil && s.State.User != nil && author.ID == s.State.User.ID {
		return false
	}
	return true
}

// shouldLogChannel returns false if logging is disabled or the channel is the
// 1984 log channel itself (avoiding feedback loops).
func (m *Module) shouldLogChannel(channelID string) bool {
	logCh := m.config.GetGamerPals1984LogChannelID()
	if logCh == "" {
		return false
	}
	return channelID != logCh
}

// channelInfo fetches the channel name and whether the channel is the text
// chat of a voice channel from the local state cache. We don't fall back to
// REST here: if the channel isn't cached it almost certainly means we can't
// see it, in which case "(unknown)" is the right value to log.
func (m *Module) channelInfo(s *discordgo.Session, channelID string) (name string, isVoiceText bool) {
	if s == nil || s.State == nil {
		return "(unknown)", false
	}
	ch, err := s.State.Channel(channelID)
	if err != nil || ch == nil {
		return "(unknown)", false
	}
	return ch.Name, ch.Type == discordgo.ChannelTypeGuildVoice || ch.Type == discordgo.ChannelTypeGuildStageVoice
}

func userFromMember(m *discordgo.Member) *discordgo.User {
	if m == nil {
		return nil
	}
	return m.User
}

// isVoiceLike returns true for voice and stage voice channel types.
func isVoiceLike(t discordgo.ChannelType) bool {
	return t == discordgo.ChannelTypeGuildVoice || t == discordgo.ChannelTypeGuildStageVoice
}

// channelTypeName returns a short label for the given channel type. Used in
// channel-event log headers.
func channelTypeName(t discordgo.ChannelType) string {
	switch t {
	case discordgo.ChannelTypeGuildVoice:
		return "voice"
	case discordgo.ChannelTypeGuildStageVoice:
		return "stage voice"
	default:
		return "channel"
	}
}

// buildChannelEventHeader formats the standard log header for channel-level
// events (creation, rename, status update). These don't have a user or
// message ID associated with them.
func buildChannelEventHeader(title, channelID, channelName, channelType string) string {
	var b strings.Builder
	b.WriteString("**")
	b.WriteString(title)
	b.WriteString("** - ")
	b.WriteString(time.Now().UTC().Format(time.RFC3339))
	b.WriteString("\n")
	fmt.Fprintf(&b, "**Channel:** <#%s> `%s` (ID: `%s`) _[%s]_", channelID, channelName, channelID, channelType)
	return b.String()
}

// buildHeader formats the standard log header with user, channel, and message
// identifiers.
func buildHeader(title string, author *discordgo.User, channelID, channelName, messageID string, isVoiceText bool) string {
	var b strings.Builder
	b.WriteString("**")
	b.WriteString(title)
	b.WriteString("** - ")
	b.WriteString(time.Now().UTC().Format(time.RFC3339))
	b.WriteString("\n")

	if author != nil {
		fmt.Fprintf(&b, "**User:** `%s` (ID: `%s`)\n", author.Username, author.ID)
	} else {
		b.WriteString("**User:** `(unknown)`\n")
	}

	suffix := ""
	if isVoiceText {
		suffix = " _(voice channel text chat)_"
	}
	fmt.Fprintf(&b, "**Channel:** <#%s> `%s` (ID: `%s`)%s", channelID, channelName, channelID, suffix)
	if messageID != "" {
		fmt.Fprintf(&b, "\n**Message ID:** `%s`", messageID)
	}
	return b.String()
}

// codeBlock renders content inside a fenced code block, truncating extreme
// content so the code block itself stays under Discord's per-message cap.
// Truncation respects UTF-8 rune boundaries so multi-byte characters
// (emoji, CJK, etc.) are never split. The full content is always also
// delivered as a file attachment by callers when appropriate.
func codeBlock(s string) string {
	return codeBlockLang("", s)
}

// codeBlockLang renders content inside a fenced code block tagged with the
// given language identifier (e.g. "diff" for syntax-highlighted diffs).
// Empty lang produces a plain fenced block. Same UTF-8 / length safety as
// codeBlock.
func codeBlockLang(lang, s string) string {
	if s == "" {
		return "```" + lang + "\n```"
	}
	display := s
	if len(display) > inlineContentCap {
		display = truncateUTF8(display, inlineContentCap) + "\n…(truncated, see attachment)"
	}
	// Neutralize embedded triple backticks so the fence isn't broken.
	display = strings.ReplaceAll(display, "```", "ʼʼʼ")
	return "```" + lang + "\n" + display + "\n```"
}

// truncateUTF8 returns s clipped to at most maxBytes bytes, backing up to the
// nearest UTF-8 rune boundary so no rune is split.
func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// renderUnifiedDiff produces a unified-diff string between before and after,
// suitable for embedding inside a ```diff``` fenced code block. Returns the
// empty string only if both inputs are equal.
func renderUnifiedDiff(before, after string) string {
	d := difflib.UnifiedDiff{
		A:        difflib.SplitLines(before),
		B:        difflib.SplitLines(after),
		FromFile: "before",
		ToFile:   "after",
		Context:  3,
	}
	out, err := difflib.GetUnifiedDiffString(d)
	if err != nil {
		return ""
	}
	return out
}

// fileAttachment is a piece of content that may be attached if too long to inline.
type fileAttachment struct {
	name    string
	content string
}

func namedFile(name, content string) fileAttachment {
	return fileAttachment{name: name, content: content}
}

// Discord API limits we conservatively guard against.
const (
	// Discord's hard cap on message content is 2000 characters; we leave
	// headroom for safety.
	maxMessageContentChars = 1900
	// Inline cap for fenced code-block bodies inside the message.
	inlineContentCap = 1500
	// Discord allows up to 10 attachments per message.
	maxAttachmentsPerMessage = 10
	// Per-attachment size cap (Discord's free upload limit is 10MB; we cap
	// well below that to be safe).
	maxAttachmentBytes = 8 * 1024 * 1024
)

// fileSpec describes a file to upload.
type fileSpec struct {
	name    string
	content []byte
}

// payload is what would actually be sent to Discord. Pure result of
// buildPayload, which keeps it easy to unit-test the limit-guard logic
// without hitting the network.
type payload struct {
	content string
	files   []fileSpec
}

// buildPayload turns a rendered message body and a set of optional
// attachments into a Discord-safe payload that:
//   - has content <= maxMessageContentChars
//   - has at most maxAttachmentsPerMessage attachments
//   - has every attachment <= maxAttachmentBytes
//
// Long attachments are uploaded as files; if the content itself is too
// long it is moved into a log.txt file and the content becomes a short
// pointer message.
func buildPayload(content string, attachments []fileAttachment) payload {
	var files []fileSpec
	for _, a := range attachments {
		if a.content == "" {
			continue
		}
		if len(a.content) > inlineContentCap {
			files = append(files, fileSpec{
				name:    a.name,
				content: clampBytes([]byte(a.content), maxAttachmentBytes),
			})
		}
	}

	if len(content) > maxMessageContentChars {
		files = append(files, fileSpec{
			name:    "log.txt",
			content: clampBytes([]byte(content), maxAttachmentBytes),
		})
		content = "📋 Log entry attached (content too long to inline)."
	}

	if len(files) > maxAttachmentsPerMessage {
		// Merge overflow files into a single combined.txt to stay within
		// Discord's per-message attachment cap.
		keep := files[:maxAttachmentsPerMessage-1]
		overflow := files[maxAttachmentsPerMessage-1:]
		var buf bytes.Buffer
		for _, f := range overflow {
			buf.WriteString("=== ")
			buf.WriteString(f.name)
			buf.WriteString(" ===\n")
			buf.Write(f.content)
			buf.WriteString("\n\n")
		}
		merged := fileSpec{
			name:    "combined.txt",
			content: clampBytes(buf.Bytes(), maxAttachmentBytes),
		}
		files = append(keep, merged)
	}

	return payload{content: content, files: files}
}

// clampBytes returns b unchanged if within max, otherwise truncates with a
// trailing marker so the receiver knows truncation occurred. Truncation
// respects UTF-8 rune boundaries (backs up if a cut would split a multi-byte
// rune). If max is too small to hold even the marker, the output is just
// truncated to max bytes (still rune-aligned) with no marker.
func clampBytes(b []byte, max int) []byte {
	if len(b) <= max {
		return b
	}
	const marker = "\n…(truncated)\n"
	if max <= len(marker) {
		cut := max
		for cut > 0 && !utf8.RuneStart(b[cut]) {
			cut--
		}
		out := make([]byte, cut)
		copy(out, b[:cut])
		return out
	}
	cut := max - len(marker)
	for cut > 0 && !utf8.RuneStart(b[cut]) {
		cut--
	}
	out := make([]byte, 0, cut+len(marker))
	out = append(out, b[:cut]...)
	out = append(out, []byte(marker)...)
	return out
}

// send posts the rendered message to the 1984 log channel. Any provided
// attachments whose content exceeds the inline cap (or whose presence is
// otherwise needed because the rendered message is itself too long) are
// uploaded as files to guarantee logging never fails for long messages.
func (m *Module) send(s *discordgo.Session, content string, attachments ...fileAttachment) {
	channelID := m.config.GetGamerPals1984LogChannelID()
	if channelID == "" {
		return
	}

	p := buildPayload(content, attachments)
	if err := m.dispatchLog(s, channelID, p); err != nil {
		m.config.Logger.Warnf("1984: failed to send log message: %v", err)
	}
}

// defaultDispatchLog is the production implementation of dispatchLog: build the
// Discord file structures and POST via ChannelMessageSendComplex.
func (m *Module) defaultDispatchLog(s *discordgo.Session, channelID string, p payload) error {
	files := make([]*discordgo.File, 0, len(p.files))
	for _, f := range p.files {
		files = append(files, &discordgo.File{
			Name:        f.name,
			ContentType: "text/plain",
			Reader:      bytes.NewReader(f.content),
		})
	}
	_, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:         p.content,
		Files:           files,
		AllowedMentions: &discordgo.MessageAllowedMentions{}, // never ping anyone from the log
	})
	return err
}
