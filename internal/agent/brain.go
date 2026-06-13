package agent

import (
	_ "embed"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/MakeNowJust/heredoc"
	"github.com/bwmarrin/discordgo"
)

// Brain holds moderator-authored guidance loaded from a Discord channel and
// injected into the agent's system prompt. It is read by run() on every
// @mention and written by the periodic/startup refresher, so access is guarded
// by a RWMutex (mirroring the agentctx registry pattern). The content is
// derived from Discord and cheaply reloaded at startup, so it is never
// persisted.
type Brain struct {
	mu       sync.RWMutex
	guidance string
}

// NewBrain returns an empty Brain.
func NewBrain() *Brain { return &Brain{} }

// Guidance returns the current guidance text, or "" when nothing is loaded.
func (b *Brain) Guidance() string {
	if b == nil {
		return ""
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.guidance
}

// set replaces the guidance.
func (b *Brain) set(guidance string) {
	b.mu.Lock()
	b.guidance = guidance
	b.mu.Unlock()
}

// brainTruncationMarker is prepended to guidance when older items were dropped
// to fit the caps, so the model knows the guidance is not exhaustive.
const brainTruncationMarker = "(Older moderator guidance was omitted to stay within the size limit.)"

//go:embed brain_guidance.md
var brainGuidanceRaw string

// brainGuidance frames the appended block. It is intentionally explicit that the
// block is data and is lower priority than the static prompt above it. It lives
// in a separate markdown file so the framing can be tuned without touching Go.
var brainGuidance = strings.TrimSpace(brainGuidanceRaw)

// assembleSystemPrompt composes the system message content: the static embedded
// prompt first (persona plus the hard Safety rules), then a clearly framed
// moderator-guidance block. When there is no guidance the static prompt is
// returned unchanged, so the agent behaves exactly as it does without the
// feature configured.
func assembleSystemPrompt(static, brain string) string {
	static = strings.TrimSpace(static)
	brain = strings.TrimSpace(brain)
	if brain == "" {
		return static
	}
	return strings.TrimSpace(heredoc.Docf(`
		%s

		%s

		%s
	`, static, brainGuidance, brain))
}

// messagesToGuidance turns channel messages (newest first, as returned by
// ChannelMessages) into a single guidance string. It skips bot and empty
// messages and uses each remaining message verbatim. The newest messages are
// kept up to the item and character caps, then rendered oldest-first so
// guidance reads chronologically. It returns the text, the number of messages
// kept, and whether anything was truncated.
func messagesToGuidance(msgs []*discordgo.Message, maxItems, maxChars int) (text string, kept int, truncated bool) {
	const sep = "\n\n"
	sepLen := utf8.RuneCountInString(sep)

	picked := make([]string, 0, maxItems) // newest first
	total := 0
	more := false

	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		if msg.Author != nil && msg.Author.Bot {
			continue
		}
		item := strings.TrimSpace(msg.Content)
		if item == "" {
			continue
		}
		addLen := utf8.RuneCountInString(item)
		if len(picked) > 0 {
			addLen += sepLen
		}
		// Always keep the newest eligible item; the final hard cap below
		// truncates it if it alone exceeds the budget. Subsequent items must
		// fit within the caps.
		if len(picked) > 0 && (len(picked) >= maxItems || total+addLen > maxChars) {
			more = true
			break
		}
		picked = append(picked, item)
		total += addLen
	}

	if len(picked) == 0 {
		return "", 0, more
	}

	// Reverse to chronological order (oldest first).
	for i, j := 0, len(picked)-1; i < j; i, j = i+1, j-1 {
		picked[i], picked[j] = picked[j], picked[i]
	}

	out := strings.Join(picked, sep)
	if r := []rune(out); len(r) > maxChars {
		out = strings.TrimSpace(string(r[:maxChars])) + " ..."
		more = true
	}
	if more {
		out = brainTruncationMarker + "\n\n" + out
	}
	return out, len(picked), more
}

// channelHiddenFromEveryone reports whether the @everyone role is explicitly
// denied View Channel on the given channel. This is the injection control: it
// keeps a public channel from feeding the prompt, so the refresher refuses to
// load unless @everyone is denied view access. The @everyone role ID equals the
// guild ID.
//
// It only checks @everyone visibility; it does not verify which other roles may
// view the channel, so a brain channel should not broadly grant view access to
// non-moderator roles.
func channelHiddenFromEveryone(ch *discordgo.Channel, guildID string) bool {
	if ch == nil || guildID == "" {
		return false
	}
	for _, ow := range ch.PermissionOverwrites {
		if ow == nil {
			continue
		}
		if ow.Type == discordgo.PermissionOverwriteTypeRole && ow.ID == guildID {
			return ow.Deny&discordgo.PermissionViewChannel != 0
		}
	}
	return false
}
