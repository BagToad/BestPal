package intro

import (
	"fmt"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/utils"
	"sort"

	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface for the intro command
type IntroModule struct {
	config *types.Dependencies
}

// New creates a new intro module
func New() *IntroModule {
	return &IntroModule{}
}

// Register adds the intro command to the command map
func (m *IntroModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	m.config = deps

	cmds["intro"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "intro",
			Description: "Look up a user's latest introduction post from the introductions forum",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "The user whose introduction to look up (defaults to yourself)",
					Required:    false,
				},
			},
		},
		HandlerFunc: m.handleIntro,
	}
}

// handleIntro handles the intro slash command
func (m *IntroModule) handleIntro(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Get the introductions forum channel ID from config
	introsChannelID := m.config.Config.GetGamerPalsIntroductionsForumChannelID()
	if introsChannelID == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
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
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Get all active threads from the forum channel
	threads, err := m.getAllActiveThreads(s, introsChannelID, i.GuildID)
	if err != nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
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
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
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
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr(postURL),
	})
}

// getAllActiveThreads gets all active threads from a forum channel
func (m *IntroModule) getAllActiveThreads(s *discordgo.Session, channelID string, guildID string) ([]*discordgo.Channel, error) {
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
		allThreads = append(allThreads, publicArchived.Threads...)
	}

	return allThreads, nil
}

// GetServices returns nil as this module has no services requiring initialization
func (m *IntroModule) Service() types.ModuleService {
return nil
}
