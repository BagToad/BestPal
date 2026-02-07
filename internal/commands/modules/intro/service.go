package intro

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/forumcache"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// IntroFeedService handles posting introductions to the feed channel
type IntroFeedService struct {
	deps *types.Dependencies
}

// NewIntroFeedService creates a new intro feed service
func NewIntroFeedService(deps *types.Dependencies) *IntroFeedService {
	return &IntroFeedService{
		deps: deps,
	}
}

// EligibilityResult contains the result of checking feed eligibility
type EligibilityResult struct {
	Eligible      bool
	TimeRemaining time.Duration
	Reason        string
}

// CheckFeedEligibility checks if a user is eligible to have their intro posted to the feed.
// This checks the database for the last time they had an intro posted.
func (s *IntroFeedService) CheckFeedEligibility(userID string) (*EligibilityResult, error) {
	if s.deps.DB == nil {
		return &EligibilityResult{
			Eligible: false,
			Reason:   "Database not available",
		}, nil
	}

	cooldownHours := s.deps.Config.GetIntroFeedRateLimitHours()
	eligible, remaining, err := s.deps.DB.IsUserEligibleForIntroFeed(userID, cooldownHours)
	if err != nil {
		return nil, fmt.Errorf("failed to check feed eligibility: %w", err)
	}

	if !eligible {
		return &EligibilityResult{
			Eligible:      false,
			TimeRemaining: remaining,
			Reason:        fmt.Sprintf("You can post to the feed again in %s", formatDuration(remaining)),
		}, nil
	}

	return &EligibilityResult{
		Eligible: true,
	}, nil
}

// ForwardThreadToFeed posts a notification about a new/bumped intro thread to the feed channel.
// Returns the message ID of the feed post, or an error.
func (s *IntroFeedService) ForwardThreadToFeed(guildID, threadID, userID, displayName, threadName string, tagIDs []string, isBump bool) (string, error) {
	feedChannelID := s.deps.Config.GetIntroFeedChannelID()
	if feedChannelID == "" {
		return "", fmt.Errorf("intro feed channel not configured")
	}

	if s.deps.Session == nil {
		return "", fmt.Errorf("discord session not available")
	}

	// Build the thread URL
	threadURL := fmt.Sprintf("https://discord.com/channels/%s/%s", guildID, threadID)

	// Resolve tag names from IDs
	tagsDisplay := s.resolveTagNames(tagIDs)

	// Get user's region from their roles
	region := s.getUserRegion(guildID, userID)

	// Get user's avatar URL (prefer server avatar over global)
	avatarURL := s.getUserAvatarURL(guildID, userID)

	// Determine title and description based on post vs bump
	title := "👋 New Introduction!"
	action := "just posted an introduction!"
	if isBump {
		title = "🔄 Introduction Bump!"
		action = "just bumped their introduction!"
	}

	// Get user's post count (excluding bumps)
	postCount := 0
	if s.deps.DB != nil {
		if count, err := s.deps.DB.GetUserIntroPostCount(userID); err != nil {
			s.deps.Config.Logger.Warnf("Failed to get intro post count for user %s: %v", userID, err)
		} else {
			postCount = count
		}
		// For new posts, include the current one in the count
		if !isBump {
			postCount++
		}
	}

	mention := fmt.Sprintf("<@%s>", userID)

	// Create the feed embed
	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: fmt.Sprintf("**%s** (%s) %s", displayName, mention, action),
		Color:       utils.Colors.Fancy(),
		Timestamp:   time.Now().Format(time.RFC3339),
		Thumbnail: &discordgo.MessageEmbedThumbnail{
			URL: avatarURL,
		},
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "📝 Title",
				Value:  threadName,
				Inline: false,
			},
			{
				Name:   "🌍 Region",
				Value:  region,
				Inline: true,
			},
			{
				Name:   "🏷️ Tags",
				Value:  tagsDisplay,
				Inline: true,
			},
			{
				Name:   "📊 # posts",
				Value:  fmt.Sprintf("%d", postCount),
				Inline: true,
			},
			{
				Name:   "🔗 Link",
				Value:  fmt.Sprintf("[Click here to view](%s)", threadURL),
				Inline: false,
			},
		},
	}

	msg, err := s.deps.Session.ChannelMessageSendEmbed(feedChannelID, embed)
	if err != nil {
		return "", fmt.Errorf("failed to send feed message: %w", err)
	}

	// Record this in the database
	if s.deps.DB != nil {
		if err := s.deps.DB.RecordIntroFeedPost(userID, threadID, msg.ID, isBump); err != nil {
			s.deps.Config.Logger.Warnf("Failed to record intro feed post: %v", err)
			// Don't return error - the message was sent successfully
		}
	}

	return msg.ID, nil
}

// HandleNewIntroThread is called when a new thread is created in the intro forum.
// It checks eligibility and forwards to the feed if appropriate.
// Silently skips if user is on cooldown (for automatic forwarding).
func (s *IntroFeedService) HandleNewIntroThread(thread *discordgo.Channel) {
	if s.deps.Session == nil || s.deps.DB == nil {
		return
	}

	// Verify this is the intro forum
	introForumID := s.deps.Config.GetGamerPalsIntroductionsForumChannelID()
	if introForumID == "" || thread.ParentID != introForumID {
		return
	}

	// Check if feed channel is configured
	feedChannelID := s.deps.Config.GetIntroFeedChannelID()
	if feedChannelID == "" {
		return
	}

	// Check eligibility (silently skip if on cooldown)
	eligibility, err := s.CheckFeedEligibility(thread.OwnerID)
	if err != nil {
		s.deps.Config.Logger.Warnf("Failed to check intro feed eligibility for user %s: %v", thread.OwnerID, err)
		return
	}

	if !eligibility.Eligible {
		s.deps.Config.Logger.Infof("Skipping intro feed for user %s: %s", thread.OwnerID, eligibility.Reason)
		// Still record the post so the post count increments
		if err := s.deps.DB.RecordIntroFeedPost(thread.OwnerID, thread.ID, "", false); err != nil {
			s.deps.Config.Logger.Warnf("Failed to record skipped intro feed post: %v", err)
		}
		return
	}

	// Get the user's display name
	member, err := s.deps.Session.GuildMember(thread.GuildID, thread.OwnerID)
	if err != nil {
		s.deps.Config.Logger.Errorf("Failed to fetch guild member for user %s: %v", thread.OwnerID, err)
		return
	}
	displayName := member.DisplayName()
	if member != nil && member.Nick != "" {
		displayName = member.Nick
	}

	// Forward to feed
	_, err = s.ForwardThreadToFeed(thread.GuildID, thread.ID, thread.OwnerID, displayName, thread.Name, thread.AppliedTags, false)
	if err != nil {
		s.deps.Config.Logger.Errorf("Failed to forward intro to feed: %v", err)
		return
	}

	s.deps.Config.Logger.Infof("Forwarded intro thread %s by %s to feed", thread.ID, thread.OwnerID)
}

// BumpIntroToFeed manually bumps an intro thread to the feed channel.
// Unlike automatic forwarding, this returns an error/message to show the user.
// If skipEligibilityCheck is true, bypasses the cooldown check (for moderators).
func (s *IntroFeedService) BumpIntroToFeed(guildID, threadID, userID, displayName, threadName string, skipEligibilityCheck bool) error {
	// Check eligibility unless bypassed
	if !skipEligibilityCheck {
		eligibility, err := s.CheckFeedEligibility(userID)
		if err != nil {
			return fmt.Errorf("failed to check eligibility: %w", err)
		}

		if !eligibility.Eligible {
			return errors.New(eligibility.Reason)
		}
	}

	// Fetch the thread to get applied tags
	var tagIDs []string
	if s.deps.Session != nil {
		thread, err := s.deps.Session.Channel(threadID)
		if err == nil && thread != nil {
			tagIDs = thread.AppliedTags
		}
	}

	// Forward to feed
	_, err := s.ForwardThreadToFeed(guildID, threadID, userID, displayName, threadName, tagIDs, true)
	if err != nil {
		return err
	}

	return nil
}

// GetUserLatestIntroThread looks up the user's latest intro thread from the cache
func (s *IntroFeedService) GetUserLatestIntroThread(userID string) (*forumcache.ThreadMeta, bool) {
	introForumID := s.deps.Config.GetGamerPalsIntroductionsForumChannelID()
	if introForumID == "" || s.deps.ForumCache == nil {
		return nil, false
	}
	return s.deps.ForumCache.GetLatestUserThread(introForumID, userID)
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60

	if hours > 0 {
		if minutes > 0 {
			return fmt.Sprintf("%dh %dm", hours, minutes)
		}
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dm", minutes)
}

// resolveTagNames converts tag IDs to their display names by looking up the forum channel
func (s *IntroFeedService) resolveTagNames(tagIDs []string) string {
	if len(tagIDs) == 0 {
		return "None"
	}

	introForumID := s.deps.Config.GetGamerPalsIntroductionsForumChannelID()
	if introForumID == "" || s.deps.Session == nil {
		return "Unknown"
	}

	// Fetch the forum channel to get available tags
	forum, err := s.deps.Session.Channel(introForumID)
	if err != nil {
		s.deps.Config.Logger.Warnf("Failed to fetch forum channel for tag resolution: %v", err)
		return "Unknown"
	}

	// Build a map of tag ID -> tag name
	tagMap := make(map[string]string)
	for _, tag := range forum.AvailableTags {
		tagMap[tag.ID] = tag.Name
	}

	// Resolve the applied tag IDs to names
	var names []string
	for _, tagID := range tagIDs {
		if name, ok := tagMap[tagID]; ok {
			names = append(names, name)
		}
	}

	if len(names) == 0 {
		return "None"
	}
	return strings.Join(names, ", ")
}

// regionRoles maps region names to their Discord role IDs
var regionRoles = map[string]string{
	"North America": "475040060786343937",
	"Europe":        "475039994554351618",
	"South America": "475040095993593866",
	"Asia":          "475040122463846422",
	"Oceania":       "505413573586059266",
	"South Africa":  "518493780308000779",
}

// getUserRegion looks up a user's region based on their roles
func (s *IntroFeedService) getUserRegion(guildID, userID string) string {
	if s.deps.Session == nil {
		return "Unknown"
	}

	member, err := s.deps.Session.GuildMember(guildID, userID)
	if err != nil {
		return "Unknown"
	}

	for region, roleID := range regionRoles {
		for _, role := range member.Roles {
			if role == roleID {
				return region
			}
		}
	}

	return "Not set"
}

// getUserAvatarURL returns the user's avatar URL, preferring server avatar over global
func (s *IntroFeedService) getUserAvatarURL(guildID, userID string) string {
	if s.deps.Session == nil {
		return ""
	}

	member, err := s.deps.Session.GuildMember(guildID, userID)
	if err != nil || member == nil {
		return ""
	}

	// Prefer server-specific avatar if set
	if member.Avatar != "" {
		return member.AvatarURL("256")
	}

	// Fall back to global user avatar
	if member.User != nil {
		return member.User.AvatarURL("256")
	}

	return ""
}
