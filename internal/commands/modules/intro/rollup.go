package intro

import (
	"fmt"
	"strings"
	"time"

	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

const rollupModel = "gpt-5-mini"

const rollupSystemPrompt = `You are a friendly community bot for GamerPals, a gaming community Discord server. You're writing a daily rollup of new introductions. Your tone should be warm, welcoming, and enthusiastic — like a friend greeting newcomers at a party. Keep it casual and fun.

Rules:
- @ mention each person who posted an introduction using the Discord format <@USER_ID>
- Welcome everyone warmly
- If you notice people who share games, interests, or other similarities, call those out in a friendly way (e.g., "Hey <@123> and <@456>, you both play Apex Legends — you should team up!")
- Don't be cold or analytical — keep it light and friendly
- Use emojis naturally but don't overdo it
- Keep the total message under 1800 characters (Discord message limit safety)
- If there are no commonalities to highlight, that's fine — just welcome everyone
- Format the message so it reads well in Discord (use bold, line breaks, etc.)`

// introEntry holds the data for a single introduction used to build the AI prompt.
type introEntry struct {
	UserID      string
	DisplayName string
	ThreadTitle string
	Body        string
}

// handleIntroductionRollup handles the /introduction-rollup slash command.
func (m *IntroModule) handleIntroductionRollup(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Defer – the AI call takes time
	_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	if m.config.DB == nil {
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ Database not available."),
		})
		return
	}

	rollupText, err := m.feedService.GenerateRollup(s, i.GuildID)
	if err != nil {
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr(fmt.Sprintf("❌ %s", err.Error())),
		})
		return
	}

	// Split and send the rollup as visible messages in the channel
	chunks := splitMessage(rollupText, 2000)
	for _, chunk := range chunks {
		_, err = s.ChannelMessageSend(i.ChannelID, chunk)
		if err != nil {
			_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{
				Content: utils.StringPtr(fmt.Sprintf("❌ Failed to send rollup message: %s", err.Error())),
			})
			return
		}
	}

	_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr("✅ Introduction rollup posted!"),
	})
}

// GenerateRollup fetches recent intros and generates an AI-powered rollup message.
// The guildID is used to resolve display names.
func (svc *IntroFeedService) GenerateRollup(s *discordgo.Session, guildID string) (string, error) {
	if svc.deps.DB == nil {
		return "", fmt.Errorf("database not available")
	}

	since := time.Now().Add(-24 * time.Hour)
	posts, err := svc.deps.DB.GetRecentIntroFeedPosts(since)
	if err != nil {
		return "", fmt.Errorf("failed to fetch recent intro posts: %w", err)
	}

	if len(posts) == 0 {
		return "☀️ No new introductions in the last 24 hours", nil
	}

	// Deduplicate by user ID (keep first occurrence's thread)
	seen := make(map[string]bool)
	var uniquePosts []struct {
		UserID   string
		ThreadID string
	}
	for _, p := range posts {
		if seen[p.UserID] {
			continue
		}
		seen[p.UserID] = true
		uniquePosts = append(uniquePosts, struct {
			UserID   string
			ThreadID string
		}{p.UserID, p.ThreadID})
	}

	// Fetch thread content for each unique user
	var entries []introEntry
	for _, p := range uniquePosts {
		// Fetch thread info — skip this user if the thread no longer exists
		thread, err := s.Channel(p.ThreadID)
		if err != nil || thread == nil {
			continue
		}

		entry := introEntry{UserID: p.UserID, ThreadTitle: thread.Name}

		// Resolve display name
		member, err := s.GuildMember(guildID, p.UserID)
		if err == nil && member != nil {
			entry.DisplayName = member.DisplayName()
			if member.Nick != "" {
				entry.DisplayName = member.Nick
			}
		} else {
			entry.DisplayName = "Unknown"
		}

		// Fetch the starter message of the thread.
		// For forum posts, the starter message ID equals the thread ID.
		msg, err := s.ChannelMessage(p.ThreadID, p.ThreadID)
		if err == nil && msg != nil {
			entry.Body = msg.Content
		}

		entries = append(entries, entry)
	}

	if len(entries) == 0 {
		return "☀️ No new introductions in the last 24 hours", nil
	}

	// Build the user prompt
	var sb strings.Builder
	sb.WriteString("Here are the introductions posted in the last 24 hours. Generate a friendly rollup welcoming these people:\n\n")
	for _, e := range entries {
		sb.WriteString("---\n")
		fmt.Fprintf(&sb, "User ID: %s\n", e.UserID)
		fmt.Fprintf(&sb, "Display Name: %s\n", e.DisplayName)
		fmt.Fprintf(&sb, "Thread Title: %s\n", e.ThreadTitle)
		sb.WriteString("Introduction:\n")
		if e.Body != "" {
			sb.WriteString(e.Body)
		} else {
			sb.WriteString("(no content available)")
		}
		sb.WriteString("\n---\n\n")
	}

	modelsClient := utils.NewModelsClient(svc.deps.Config)
	result := modelsClient.ModelsRequest(rollupSystemPrompt, sb.String(), rollupModel)
	if result == "" {
		return "", fmt.Errorf("model request failed; try again later")
	}

	return result, nil
}

// splitMessage splits text into chunks of at most maxLen runes,
// preferring to break at newline boundaries.
func splitMessage(text string, maxLen int) []string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(runes) > 0 {
		if len(runes) <= maxLen {
			chunks = append(chunks, string(runes))
			break
		}

		// Look for the last newline within the rune limit
		limit := maxLen
		if limit > len(runes) {
			limit = len(runes)
		}
		cut := -1
		for i := limit - 1; i >= 0; i-- {
			if runes[i] == '\n' {
				cut = i + 1 // include the newline in the current chunk
				break
			}
		}
		if cut <= 0 {
			// No newline found — hard cut at maxLen runes
			cut = limit
		}

		chunks = append(chunks, string(runes[:cut]))
		runes = runes[cut:]
	}
	return chunks
}
