package scamguard

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// maxTimeout is Discord's hard cap on member timeout duration.
const maxTimeout = 28 * 24 * time.Hour

// enforce applies the configured action for a detected scam image and logs it.
// Actions are cumulative: "log" only logs; "delete" also deletes the message;
// "timeout" also times the author out.
func (m *Module) enforce(s *discordgo.Session, e *discordgo.MessageCreate, matched string) {
	action := m.config.GetScamGuardAction()

	deleted := false
	if action == "delete" || action == "timeout" {
		if err := m.deleteMessage(s, e.ChannelID, e.ID); err != nil {
			m.config.Logger.Warnf("scamguard: failed to delete message %s: %v", e.ID, err)
		} else {
			deleted = true
		}
	}

	timedOut := false
	var appliedTimeout time.Duration
	if action == "timeout" {
		dur := min(m.config.GetScamGuardTimeoutDuration(), maxTimeout)
		appliedTimeout = dur
		until := time.Now().Add(dur)
		if err := m.timeoutMember(s, e.GuildID, e.Author.ID, &until); err != nil {
			m.config.Logger.Warnf("scamguard: failed to timeout user %s: %v", e.Author.ID, err)
		} else {
			timedOut = true
		}
	}

	m.logAction(s, e, matched, deleted, timedOut, appliedTimeout)
}

// logAction posts a mod-channel embed describing the detection and what was
// done. timeoutDur is the effective (clamped) timeout applied, used only for
// display.
func (m *Module) logAction(s *discordgo.Session, e *discordgo.MessageCreate, matched string, deleted, timedOut bool, timeoutDur time.Duration) {
	channelID := m.config.GetScamGuardLogChannelID()
	if channelID == "" {
		return
	}

	outcome := "Logged only"
	switch {
	case deleted && timedOut:
		outcome = fmt.Sprintf("Message deleted; user timed out for %s", timeoutDur)
	case timedOut:
		outcome = fmt.Sprintf("User timed out for %s; message delete failed", timeoutDur)
	case deleted:
		outcome = "Message deleted"
	}

	fields := []*discordgo.MessageEmbedField{
		{Name: "User", Value: fmt.Sprintf("<@%s> (%s)", e.Author.ID, e.Author.ID), Inline: true},
		{Name: "Channel", Value: fmt.Sprintf("<#%s>", e.ChannelID), Inline: true},
		{Name: "Message ID", Value: fmt.Sprintf("`%s`", e.ID), Inline: true},
		{Name: "Action", Value: outcome, Inline: false},
		{Name: "Matched Hash", Value: fmt.Sprintf("`%s`", matched), Inline: false},
	}

	embed := &discordgo.MessageEmbed{
		Title:     "🛡️ Scam Image Detected",
		Color:     0xd33f49,
		Fields:    fields,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	if err := m.sendLogEmbed(s, channelID, embed); err != nil {
		m.config.Logger.Warnf("scamguard: failed to send log embed: %v", err)
	}
}

// logUnmark posts a mod-channel audit embed when blocklist entries are removed
// via the "Unmark Scam Image" command, so removals are as traceable as adds.
func (m *Module) logUnmark(s *discordgo.Session, modID string, res unmarkResult) {
	channelID := m.config.GetScamGuardLogChannelID()
	if channelID == "" {
		return
	}

	moderator := "unknown"
	if modID != "" {
		moderator = fmt.Sprintf("<@%s> (%s)", modID, modID)
	}

	hashList := strings.Join(res.hashes, "\n")
	if hashList == "" {
		hashList = "(none)"
	}
	const maxHashField = 1000
	if len(hashList) > maxHashField {
		hashList = hashList[:maxHashField] + "\n..."
	}

	embed := &discordgo.MessageEmbed{
		Title: "🧹 Scam Image Unmarked",
		Color: 0x3ba55d,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Moderator", Value: moderator, Inline: true},
			{Name: "Entries Removed", Value: fmt.Sprintf("%d", res.removed), Inline: true},
			{Name: "Hashes", Value: fmt.Sprintf("```\n%s\n```", hashList), Inline: false},
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	if err := m.sendLogEmbed(s, channelID, embed); err != nil {
		m.config.Logger.Warnf("scamguard: failed to send unmark log embed: %v", err)
	}
}
