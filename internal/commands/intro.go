package commands

import (
	"fmt"
	"gamerpal/internal/utils"
	"sort"

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
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
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

	s.InteractionResponseDelete(i.Interaction)

	// Respond with just the link as requested
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: postURL,
		},
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
