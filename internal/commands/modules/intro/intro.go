package intro

import (
	"fmt"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/utils"
	"sort"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface for the intro command
type IntroModule struct {
	config *types.Dependencies
}

// New creates a new intro module
func New(deps *types.Dependencies) *IntroModule {
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

	// Attempt fast-path via shared ForumCache
	if m.config.ForumCache != nil {
		if meta, ok := m.config.ForumCache.GetLatestUserThread(introsChannelID, targetUser.ID); ok && meta != nil {
			postURL := fmt.Sprintf("https://discord.com/channels/%s/%s", i.GuildID, meta.ID)
			_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr(postURL)})
			return
		}
		// If miss, opportunistically trigger a refresh (best‑effort, async)
		go func(guildID, forumID, userID string) {
			_ = m.config.ForumCache.RefreshForum(guildID, forumID)
		}(i.GuildID, introsChannelID, targetUser.ID)
	}

	// Fallback: existing slower scan logic (active + archived)
	threads, err := m.getAllActiveThreads(s, introsChannelID, i.GuildID)
	if err != nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr(fmt.Sprintf("❌ Error accessing introductions forum: %v", err))})
		return
	}
	var userThreads []*discordgo.Channel
	for _, thread := range threads {
		if thread.OwnerID == targetUser.ID {
			userThreads = append(userThreads, thread)
		}
	}
	if len(userThreads) == 0 {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr(fmt.Sprintf("❌ No introduction post found for %s.", targetUser.Mention()))})
		return
	}
	sort.Slice(userThreads, func(iIdx, jIdx int) bool { return userThreads[iIdx].ID > userThreads[jIdx].ID })
	latestThread := userThreads[0]
	postURL := fmt.Sprintf("https://discord.com/channels/%s/%s", i.GuildID, latestThread.ID)
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr(postURL)})

	// Seed cache with discovered thread if cache exists and miss just occurred
	if m.config.ForumCache != nil {
		go func(metaThreadID, forumID, guildID, ownerID string) {
			// Minimal single-thread refresh approach: full refresh to be consistent
			_ = m.config.ForumCache.RefreshForum(guildID, forumID)
		}(latestThread.ID, introsChannelID, i.GuildID, targetUser.ID)
	}
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

	// Try to get archived threads if available (single page best-effort)
	publicArchived, err := s.ThreadsArchived(channelID, nil, 50)
	if err == nil && publicArchived != nil {
		allThreads = append(allThreads, publicArchived.Threads...)
	}

	// Sort by creation just once for determinism in fallback path (snowflake order)
	sort.Slice(allThreads, func(iA, jA int) bool { return allThreads[iA].ID > allThreads[jA].ID })

	// Artificial minor sleep to reduce immediate hammering on large forums if invoked repeatedly (rudimentary backoff)
	time.Sleep(50 * time.Millisecond)

	return allThreads, nil
}

// Service returns nil as this module has no services requiring initialization
func (m *IntroModule) Service() types.ModuleService {
	return nil
}
