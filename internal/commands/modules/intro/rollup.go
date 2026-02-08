package intro

import (
	"fmt"
	"strings"
	"time"

	"gamerpal/internal/utils"

	"github.com/MakeNowJust/heredoc"
	"github.com/bwmarrin/discordgo"
)

const rollupModel = "openai/gpt-5"

var rollupSystemPrompt = heredoc.Doc(`
	You are a friendly community bot for GamerPals, a gaming community Discord server. 
	You're writing a daily rollup of new introductions.
	Your tone should be warm like a friend greeting newcomers at a party. Keep it cool, casual, and fun. Be very brief. 

	Rules:
	- @ mention each person who posted an introduction using the Discord format <@USER_ID>
	- If you notice people who share games, interests, or other similarities, call those out in a friendly way (e.g., "Hey <@123> and <@456>, you both play Apex Legends, you should team up!")
	- Don't be cold or analytical, keep it light and friendly
	- Use 1 or 2 emojis naturally but don't overdo it
	- Keep the total message under 1800 characters (Discord message limit safety)
	- If there are no commonalities to highlight, that's fine, just welcome everyone
	- Format the message so it reads well in Discord (use bold, line breaks, etc.)
	- Use punctuation like semicolons instead of em-dashes and large hyphens.
	- Personalized messages should be extremely brief, just 1 short sentence referencing something from their intro (e.g., "likes retro games." or "Into Apex at the moment.").

	Use this template:

	## Welcoming some new pals! :frog:

	- @USER1: <very short 1 sentence personalized message>
	- @USER2: <very short 1 sentence personalized message>
	- @USER3: <very short 1 sentence personalized message>

	@USER1 and @USER2, <personalized message based on their intro content and any commonalities>
`)

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

	rollupText, mentionedUserIDs, err := m.feedService.GenerateRollup(s, i.GuildID)
	if err != nil {
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr(fmt.Sprintf("❌ %s", err.Error())),
		})
		return
	}

	// Only allow mentions for the specific newcomer user IDs
	allowedMentions := &discordgo.MessageAllowedMentions{
		Users: mentionedUserIDs,
	}

	// Split and send the rollup as visible messages in the channel
	chunks := splitMessage(rollupText, 2000)
	for _, chunk := range chunks {
		_, err = s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
			Content:         chunk,
			AllowedMentions: allowedMentions,
		})
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
// Returns the rollup text and the list of user IDs mentioned in the rollup.
func (svc *IntroFeedService) GenerateRollup(s *discordgo.Session, guildID string) (string, []string, error) {
	if svc.deps.DB == nil {
		return "", nil, fmt.Errorf("database not available")
	}

	since := time.Now().UTC().Add(-24 * time.Hour)
	svc.deps.Config.Logger.Infof("[Rollup] Querying intro_feed_posts since %s", since.Format("2006-01-02 15:04:05"))

	posts, err := svc.deps.DB.GetRecentIntroFeedPosts(since)
	if err != nil {
		return "", nil, fmt.Errorf("failed to fetch recent intro posts: %w", err)
	}

	svc.deps.Config.Logger.Infof("[Rollup] Found %d posts from DB", len(posts))
	for i, p := range posts {
		svc.deps.Config.Logger.Infof("[Rollup]   post[%d]: user=%s thread=%s isBump=%v postedAt=%v", i, p.UserID, p.ThreadID, p.IsBump, p.PostedAt)
	}

	if len(posts) == 0 {
		return "☀️ No new introductions in the last 24 hours", nil, nil
	}

	// Deduplicate to unique user IDs
	seen := make(map[string]bool)
	var uniqueUserIDs []string
	for _, p := range posts {
		if seen[p.UserID] {
			continue
		}
		seen[p.UserID] = true
		uniqueUserIDs = append(uniqueUserIDs, p.UserID)
	}

	svc.deps.Config.Logger.Infof("[Rollup] %d unique users after dedup", len(uniqueUserIDs))

	// Resolve each user's latest intro thread via the forum cache,
	// which tracks the current (non-deleted) thread per user.
	introForumID := svc.deps.Config.GetGamerPalsIntroductionsForumChannelID()
	var entries []introEntry
	for _, userID := range uniqueUserIDs {
		meta, ok := svc.deps.ForumCache.GetLatestUserThread(introForumID, userID)
		if !ok || meta == nil {
			svc.deps.Config.Logger.Warnf("[Rollup] No cached thread for user %s, skipping", userID)
			continue
		}

		entry := introEntry{UserID: userID, ThreadTitle: meta.Name}

		// Resolve display name
		member, err := s.GuildMember(guildID, userID)
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
		msg, err := s.ChannelMessage(meta.ID, meta.ID)
		if err == nil && msg != nil {
			entry.Body = msg.Content
		} else {
			svc.deps.Config.Logger.Warnf("[Rollup] s.ChannelMessage(%s, %s) failed: %v", meta.ID, meta.ID, err)
		}

		entries = append(entries, entry)
	}

	svc.deps.Config.Logger.Infof("[Rollup] %d entries after thread fetch", len(entries))

	if len(entries) == 0 {
		return "☀️ No new unique introductions in the last 24 hours", nil, nil
	}

	// Collect mentioned user IDs
	var mentionedUserIDs []string
	for _, e := range entries {
		mentionedUserIDs = append(mentionedUserIDs, e.UserID)
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
		return "", nil, fmt.Errorf("model request failed; try again later")
	}

	return result, mentionedUserIDs, nil
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
