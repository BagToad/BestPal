package commands

import (
	"fmt"
	"gamerpal/internal/utils"
	"sort"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
)

// handleIntro handles the intro slash command
func (h *SlashHandler) handleIntro(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Get the introductions forum channel ID from config
	introsChannelID := h.config.GetGamerPalsIntroductionsForumChannelID()
	if introsChannelID == "" {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Introductions forum channel is not configured.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Get the target user (defaults to the command invoker if not specified)
	var targetUser *discordgo.User
	options := i.ApplicationCommandData().Options
	if len(options) > 0 && options[0].Name == "user" {
		targetUser = options[0].UserValue(s)
	} else {
		targetUser = i.Member.User
	}

	// Acknowledge the interaction immediately as this might take time
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Get all active threads from the forum channel
	threads, err := getAllActiveThreads(s, introsChannelID, i.GuildID)
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr(fmt.Sprintf("❌ Error accessing introductions forum: %v", err)),
		})
		return
	}

	// Find threads created by the target user
	var userThreads []*discordgo.Channel
	for _, thread := range threads {
		if thread.OwnerID == targetUser.ID {
			userThreads = append(userThreads, thread)
		}
	}

	if len(userThreads) == 0 {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr(fmt.Sprintf("❌ No introduction post found for %s.", targetUser.Mention())),
		})
		return
	}

	// Sort threads by creation time (newest first)
	sort.Slice(userThreads, func(i, j int) bool {
		// Use the ID for sorting since newer Discord channels have larger IDs
		return userThreads[i].ID > userThreads[j].ID
	})

	// Get the latest thread
	latestThread := userThreads[0]

	// Create the direct link to the forum post
	postURL := fmt.Sprintf("https://discord.com/channels/%s/%s", i.GuildID, latestThread.ID)

	// Respond with just the link as requested
	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr(postURL),
	})
}

// getAllActiveThreads gets all active threads from a forum channel
func getAllActiveThreads(s *discordgo.Session, channelID string, guildID string) ([]*discordgo.Channel, error) {
	var allThreads []*discordgo.Channel

	// For forum channels, get the channel and its threads directly
	// Note: Discord API might require different approaches depending on the version
	// Let's try to get the channel itself first to verify it's a forum channel
	channel, err := s.Channel(channelID)
	if err != nil {
		return nil, fmt.Errorf("failed to get channel: %w", err)
	}

	if channel.Type != discordgo.ChannelTypeGuildForum {
		return nil, fmt.Errorf("channel %s is not a forum channel", channelID)
	}

	// Get active threads that are part of this forum
	activeThreads, err := s.GuildThreadsActive(guildID)
	if err != nil {
		// If we can't get active threads, try a different approach
		return nil, fmt.Errorf("failed to get active threads: %w", err)
	}

	// Filter threads that belong to our forum channel
	for _, thread := range activeThreads.Threads {
		if thread.ParentID == channelID {
			allThreads = append(allThreads, thread)
		}
	}

	// Try to get archived threads if available
	publicArchived, err := s.ThreadsArchived(channelID, nil, 50)
	if err == nil && publicArchived != nil {
		for _, thread := range publicArchived.Threads {
			allThreads = append(allThreads, thread)
		}
	}

	return allThreads, nil
}

// handlePruneForum scans a forum channel for threads whose starter was deleted.
// Dry run by default; when execute:true, deletes flagged threads.
func (h *SlashHandler) handlePruneForum(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Admin guard
	if !utils.HasAdminPermissions(s, i) {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ You need administrator permissions to run this command.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	var forumID string
	var forumChannel *discordgo.Channel
	execute := false
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "forum" {
			forumChannel = opt.ChannelValue(s)
			if forumChannel == nil {
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "❌ Unable to resolve the provided channel.",
						Flags:   discordgo.MessageFlagsEphemeral,
					},
				})
				return
			}
			forumID = forumChannel.ID
			// Also runtime-validate it's a forum
			if forumChannel.Type != discordgo.ChannelTypeGuildForum {
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "❌ The selected channel is not a forum channel.",
						Flags:   discordgo.MessageFlagsEphemeral,
					},
				})
				return
			}
		}
		if opt.Name == "execute" {
			execute = opt.BoolValue()
		}
	}

	if forumID == "" {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ You must provide a forum channel to prune.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Defer response
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Fetch threads under the forum
	threads, err := getAllActiveThreads(s, forumID, i.GuildID)
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr(fmt.Sprintf("❌ Error accessing forum: %v", err)),
		})
		return
	}

	type flagged struct {
		thread *discordgo.Channel
		reason string
	}

	var flaggedThreads []flagged
	var unknownThreads []flagged

	for _, th := range threads {
		if !th.IsThread() {
			continue
		}

		maxMessages := 500

		msgs, err := fetchMessagesLimited(s, th.ID, maxMessages)
		if err != nil {
			h.config.Logger.Warnf("Error fetching messages for thread %s: %v", th.ID, err)
			continue
		}
		if len(msgs) == 0 {
			flaggedThreads = append(flaggedThreads, flagged{thread: th, reason: "no messages remain"})
			continue
		}

		threadCreated, err := snowflakeTime(th.ID)
		if err != nil {
			h.config.Logger.Warnf("Unable to parse snowflake for thread %s: %v", th.ID, err)
		}

		hasOwnerMessage := false
		for _, m := range msgs {
			if m.Author != nil && m.Author.ID == th.OwnerID {
				hasOwnerMessage = true
				break
			}
		}

		// Only do this if the thread has a reasonable number of messages
		// I don't think this actually works though since the docs say MessageCount
		// tops out at 50...
		if forumChannel.MessageCount <= maxMessages {
			oldest := msgs[len(msgs)-1]
			if oldest.Author == nil || oldest.Author.ID != th.OwnerID {
				flaggedThreads = append(flaggedThreads, flagged{thread: th, reason: "starter missing (oldest message not by owner)"})
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if !threadCreated.IsZero() {
				gap := oldest.Timestamp.Sub(threadCreated)
				if gap > 2*time.Second {
					flaggedThreads = append(flaggedThreads, flagged{thread: th, reason: fmt.Sprintf("starter missing (creation gap %s)", gap.Truncate(time.Second))})
					time.Sleep(50 * time.Millisecond)
					continue
				}
			}
			if !hasOwnerMessage {
				flaggedThreads = append(flaggedThreads, flagged{thread: th, reason: "owner has no messages in thread"})
			}
		} else {
			unknownThreads = append(unknownThreads, flagged{thread: th, reason: "Thread too long"})
		}

		time.Sleep(50 * time.Millisecond)
	}

	sort.Slice(flaggedThreads, func(a, b int) bool { return flaggedThreads[a].thread.ID > flaggedThreads[b].thread.ID })

	deletedCount := 0
	failedCount := 0
	if execute {
		for _, f := range flaggedThreads {
			if _, err := s.ChannelDelete(f.thread.ID); err != nil {
				failedCount++
				h.config.Logger.Warnf("Failed deleting thread %s: %v", f.thread.ID, err)
				time.Sleep(100 * time.Millisecond)
				continue
			}
			deletedCount++
			time.Sleep(150 * time.Millisecond)
		}
	}

	mode := "Dry Run"
	color := utils.Colors.Info()
	if execute {
		mode = "Executed"
		color = utils.Colors.Warning()
	}

	description := fmt.Sprintf("Mode: %s\nForum: <#%s>\nThreads scanned: %d\nThreads flagged: %d\nThreads unknown: %d", mode, forumID, len(threads), len(flaggedThreads), len(unknownThreads))
	if execute {
		description += fmt.Sprintf("\nThreads deleted: %d\nDelete failures: %d", deletedCount, failedCount)
	}

	maxList := 20
	fieldValue := ""
	allThreads := append(flaggedThreads, unknownThreads...)
	for idx, f := range allThreads {
		if idx >= maxList {
			fieldValue += fmt.Sprintf("\n…and %d more", len(allThreads)-maxList)
			break
		}
		fieldValue += fmt.Sprintf("• <#%s> — %s\n", f.thread.ID, f.reason)
	}

	fields := []*discordgo.MessageEmbedField{}
	if len(flaggedThreads) > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   "Flagged Threads",
			Value:  fieldValue,
			Inline: false,
		})
	}

	embed := &discordgo.MessageEmbed{
		Title:       "Forum Prune Report",
		Description: description,
		Color:       color,
		Fields:      fields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Use /prune-forum forum:<#%s> execute:true to delete flagged threads", forumID),
		},
	}

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	})
}

// fetchMessagesLimited retrieves up to 'limitTotal' messages from a channel using pagination
func fetchMessagesLimited(s *discordgo.Session, channelID string, limitTotal int) ([]*discordgo.Message, error) {
	var all []*discordgo.Message
	var beforeID string
	const page = 100

	for {
		if limitTotal > 0 && len(all) >= limitTotal {
			break
		}
		fetch := page
		if limitTotal > 0 && limitTotal-len(all) < page {
			fetch = limitTotal - len(all)
		}
		if fetch <= 0 {
			break
		}
		msgs, err := s.ChannelMessages(channelID, fetch, beforeID, "", "")
		if err != nil {
			return nil, err
		}
		if len(msgs) == 0 {
			break
		}
		all = append(all, msgs...)
		beforeID = msgs[len(msgs)-1].ID
		if len(msgs) < fetch {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	return all, nil
}

// snowflakeTime converts a Discord snowflake ID string into its creation time.
func snowflakeTime(id string) (time.Time, error) {
	// Discord epoch (ms) = 2015-01-01T00:00:00.000Z
	const discordEpochMS int64 = 1420070400000
	v, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	ms := int64(v>>22) + discordEpochMS
	return time.Unix(0, ms*int64(time.Millisecond)), nil
}
