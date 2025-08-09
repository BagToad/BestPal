package commands

import (
	"fmt"
	"gamerpal/internal/utils"
	"sort"
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

// handleIntroPrune scans the introductions forum for threads where the original post was deleted
// and deletes the entire thread when execute:true. Dry-run (default) only reports findings.
func (h *SlashHandler) handleIntroPrune(s *discordgo.Session, i *discordgo.InteractionCreate) {
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

	// Parse execute option (default: false)
	execute := false
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "execute" {
			execute = opt.BoolValue()
			break
		}
	}

	// Defer response
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Fetch all threads under the forum
	threads, err := getAllActiveThreads(s, introsChannelID, i.GuildID)
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr(fmt.Sprintf("❌ Error accessing introductions forum: %v", err)),
		})
		return
	}

	type flagged struct {
		thread *discordgo.Channel
		reason string
	}

	var flaggedThreads []flagged

	// Scan each thread; look for any message authored by the thread owner
	for _, th := range threads {
		// Skip if somehow not a thread
		if !th.IsThread() {
			continue
		}

		msgs, err := fetchMessagesLimited(s, th.ID, 300)
		if err != nil {
			// On error, skip this thread but note it
			h.config.Logger.Warnf("Error fetching messages for thread %s: %v", th.ID, err)
			continue
		}

		// If there are no messages at all, it's clearly orphaned
		if len(msgs) == 0 {
			flaggedThreads = append(flaggedThreads, flagged{thread: th, reason: "no messages remain"})
			continue
		}

		// Check if any message is authored by the thread owner
		hasOwnerMessage := false
		for _, m := range msgs {
			if m.Author != nil && m.Author.ID == th.OwnerID {
				hasOwnerMessage = true
				break
			}
		}
		if !hasOwnerMessage {
			flaggedThreads = append(flaggedThreads, flagged{thread: th, reason: "owner's post missing"})
		}

		// Be gentle with rate limits when scanning many threads
		time.Sleep(50 * time.Millisecond)
	}

	// Sort by newest first (by ID)
	sort.Slice(flaggedThreads, func(a, b int) bool { return flaggedThreads[a].thread.ID > flaggedThreads[b].thread.ID })

	deletedCount := 0
	failedCount := 0

	if execute {
		for _, f := range flaggedThreads {
			if _, err := s.ChannelDelete(f.thread.ID); err != nil {
				failedCount++
				h.config.Logger.Warnf("Failed deleting thread %s: %v", f.thread.ID, err)
				// small delay to avoid hammering on failures
				time.Sleep(100 * time.Millisecond)
				continue
			}
			deletedCount++
			time.Sleep(150 * time.Millisecond)
		}
	}

	// Build response embed
	mode := "Dry Run"
	color := utils.Colors.Info()
	if execute {
		mode = "Executed"
		color = utils.Colors.Warning()
	}

	description := fmt.Sprintf("Mode: %s\nThreads scanned: %d\nThreads flagged: %d", mode, len(threads), len(flaggedThreads))
	if execute {
		description += fmt.Sprintf("\nThreads deleted: %d\nDelete failures: %d", deletedCount, failedCount)
	}

	// List up to 20 flagged threads with reasons
	maxList := 20
	fieldValue := ""
	for idx, f := range flaggedThreads {
		if idx >= maxList {
			fieldValue += fmt.Sprintf("\n…and %d more", len(flaggedThreads)-maxList)
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
		Title:       "Introductions Prune Report",
		Description: description,
		Color:       color,
		Fields:      fields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Use /intro-prune execute:true to delete flagged threads",
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
