package intro

import (
	"errors"
	"fmt"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/forumcache"

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
func (s *IntroFeedService) ForwardThreadToFeed(guildID, threadID, userID, displayName string) (string, error) {
	feedChannelID := s.deps.Config.GetIntroFeedChannelID()
	if feedChannelID == "" {
		return "", fmt.Errorf("intro feed channel not configured")
	}

	if s.deps.Session == nil {
		return "", fmt.Errorf("discord session not available")
	}

	// Build the thread URL
	threadURL := fmt.Sprintf("https://discord.com/channels/%s/%s", guildID, threadID)

	// Create the feed message
	content := fmt.Sprintf("👋 **%s** just posted an introduction!\n%s", displayName, threadURL)

	msg, err := s.deps.Session.ChannelMessageSend(feedChannelID, content)
	if err != nil {
		return "", fmt.Errorf("failed to send feed message: %w", err)
	}

	// Record this in the database
	if s.deps.DB != nil {
		if err := s.deps.DB.RecordIntroFeedPost(userID, threadID, msg.ID); err != nil {
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
		return
	}

	// Get the user's display name
	displayName := getDisplayName(s.deps.Session, thread.GuildID, thread.OwnerID)

	// Forward to feed
	_, err = s.ForwardThreadToFeed(thread.GuildID, thread.ID, thread.OwnerID, displayName)
	if err != nil {
		s.deps.Config.Logger.Errorf("Failed to forward intro to feed: %v", err)
		return
	}

	s.deps.Config.Logger.Infof("Forwarded intro thread %s by %s to feed", thread.ID, thread.OwnerID)
}

// BumpIntroToFeed manually bumps an intro thread to the feed channel.
// Unlike automatic forwarding, this returns an error/message to show the user.
// If skipEligibilityCheck is true, bypasses the cooldown check (for moderators).
func (s *IntroFeedService) BumpIntroToFeed(guildID, threadID, userID, displayName string, skipEligibilityCheck bool) error {
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

	// Forward to feed
	_, err := s.ForwardThreadToFeed(guildID, threadID, userID, displayName)
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

// getDisplayName gets a user's display name (nickname or username)
func getDisplayName(session *discordgo.Session, guildID, userID string) string {
	member, err := session.GuildMember(guildID, userID)
	if err != nil {
		// Fallback to just fetching the user
		user, err := session.User(userID)
		if err != nil {
			return "Someone"
		}
		return user.Username
	}

	if member.Nick != "" {
		return member.Nick
	}
	if member.User != nil {
		return member.User.Username
	}
	return "Someone"
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
