package intro

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Refresh the existing helper, never reconstruct it: results and the lookup button
// (including loading state, IDs and style) belong to the fetched component tree.
func (s *IntroFeedService) refreshIntroCooldownReset(guildID, threadID, userID string, postedAt time.Time, isAdmin bool) error {
	var reset time.Time
	if !isAdmin {
		if postedAt.IsZero() {
			return fmt.Errorf("successful feed timestamp is unavailable")
		}
		member, err := s.deps.Session.GuildMember(guildID, userID)
		if err != nil {
			return fmt.Errorf("resolve cooldown member: %w", err)
		}
		if member == nil {
			return fmt.Errorf("cooldown member is unavailable")
		}
		reset = expectedCooldownReset(postedAt, s.cooldownHoursForMember(member), introCooldownSchedule)
	}
	message, err := s.findAutoIntroComment(threadID)
	if err != nil {
		return err
	}
	preamble := message.Components[0].(*discordgo.TextDisplay)
	updated := withExpectedCooldownReset(preamble.Content, reset)
	if updated == preamble.Content {
		return nil
	}
	preamble.Content = updated
	_, err = s.deps.Session.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID: message.ID, Channel: threadID, Components: &message.Components,
	})
	if err != nil {
		return fmt.Errorf("edit intro helper: %w", err)
	}
	return nil
}

// There is no persisted helper-message ID. Inspect only the first 100 replies
// after the forum starter, where the automatic helper is normally posted.
// Missing or ambiguous helpers are warning-only; never create a replacement.
func (s *IntroFeedService) findAutoIntroComment(threadID string) (*discordgo.Message, error) {
	session := s.deps.Session
	if session.State == nil || session.State.User == nil || session.State.User.ID == "" {
		return nil, fmt.Errorf("bot identity is unavailable")
	}
	messages, err := session.ChannelMessages(threadID, 100, "", threadID, "")
	if err != nil {
		return nil, fmt.Errorf("read intro replies: %w", err)
	}
	var found *discordgo.Message
	for _, message := range messages {
		if message == nil || message.ID == "" || message.ChannelID != threadID || message.Author == nil ||
			message.Author.ID != session.State.User.ID || !message.Author.Bot || message.WebhookID != "" {
			continue
		}
		// Unlike the internal lookup handler, this is an untrusted message-list boundary.
		if len(message.Components) != 2 && len(message.Components) != 3 {
			continue
		}
		preamble, ok := message.Components[0].(*discordgo.TextDisplay)
		if !ok || !strings.HasPrefix(preamble.Content, "💥 Your intro is up on [the feed](https://discord.com/channels/") ||
			!strings.Contains(preamble.Content, "\n`/bump-intro` - repost to the feed") {
			continue
		}
		row, ok := message.Components[1].(*discordgo.ActionsRow)
		if !ok || len(row.Components) != 1 {
			continue
		}
		button, ok := row.Components[0].(*discordgo.Button)
		if !ok || button.CustomID != LookupGameThreadsCustomID {
			continue
		}
		if len(message.Components) == 3 {
			if _, ok := message.Components[2].(*discordgo.TextDisplay); !ok {
				continue
			}
		}
		if found != nil {
			return nil, fmt.Errorf("multiple intro helpers found in the first 100 replies")
		}
		found = message
	}
	if found == nil {
		return nil, fmt.Errorf("intro helper not found in the first 100 replies")
	}
	return found, nil
}
