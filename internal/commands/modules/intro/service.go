package intro

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/forumcache"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// IntroFeedService handles posting introductions to the feed channel
type IntroFeedService struct {
	types.BaseService
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
// This checks the database for the last time they had an intro posted. Server (Nitro) boosters
// use a separate rate limit when one is configured (see feedCooldownHours).
func (s *IntroFeedService) CheckFeedEligibility(guildID, userID string) (*EligibilityResult, error) {
	if s.deps.DB == nil {
		return &EligibilityResult{
			Eligible: false,
			Reason:   "Database not available",
		}, nil
	}

	cooldownHours := s.feedCooldownHours(guildID, userID)
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

// feedCooldownHours returns the rate-limit window (in hours) that applies to a user. When a
// booster rate limit is configured and the user is a server booster, the booster window is used;
// otherwise the standard window applies. The member is only fetched when a booster limit is set.
func (s *IntroFeedService) feedCooldownHours(guildID, userID string) int {
	boosterHours := s.deps.Config.GetIntroFeedBoosterRateLimitHours()
	if boosterHours <= 0 || s.deps.Session == nil {
		return s.deps.Config.GetIntroFeedRateLimitHours()
	}

	member, err := s.deps.Session.GuildMember(guildID, userID)
	if err != nil {
		s.deps.Config.Logger.Warnf("Failed to fetch member %s for booster rate limit check: %v", userID, err)
		return s.deps.Config.GetIntroFeedRateLimitHours()
	}

	return s.cooldownHoursForMember(member)
}

// cooldownHoursForMember returns the applicable feed cooldown for an already-resolved member,
// preferring the booster rate limit when configured and the member is a server booster.
func (s *IntroFeedService) cooldownHoursForMember(member *discordgo.Member) int {
	boosterHours := s.deps.Config.GetIntroFeedBoosterRateLimitHours()
	if boosterHours > 0 && s.memberIsBooster(member) {
		return boosterHours
	}
	return s.deps.Config.GetIntroFeedRateLimitHours()
}

// memberIsBooster reports whether a guild member is a server (Nitro) booster, as reported by the
// Discord API's premium_since field.
func (s *IntroFeedService) memberIsBooster(member *discordgo.Member) bool {
	return member != nil && member.PremiumSince != nil
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
// It forwards the intro to the feed and posts a helper message in-thread.
func (s *IntroFeedService) HandleNewIntroThread(thread *discordgo.Channel) {
	if s.deps.Session == nil || thread == nil {
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

	// Require the configured availability role. This keeps deployments that have
	// not completed the role setup from bypassing the posting cooldown.
	if s.deps.Config.ForGuild(thread.GuildID).GetIntroAvailableRoleID() == "" {
		s.deps.Config.Logger.Warnf("Skipping intro feed forwarding for thread %s: intro available role is not configured", thread.ID)
		return
	}

	// Get the user's display name
	member, err := s.deps.Session.GuildMember(thread.GuildID, thread.OwnerID)
	if err != nil || member == nil {
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

	// Consume intro availability only after the post is successfully forwarded.
	if err := s.removeIntroAvailableRoleIfPresent(thread.GuildID, thread.OwnerID); err != nil {
		s.deps.Config.Logger.Warnf("[IntroAvailable] Failed to remove role after intro forward for user %s: %v", thread.OwnerID, err)
	}

	s.deps.Config.Logger.Infof("Forwarded intro thread %s by %s to feed", thread.ID, thread.OwnerID)

	// Post auto-post in the intro thread
	autoIntroComment := newAutoIntroComment(thread.GuildID, feedChannelID)
	_, err = s.deps.Session.ChannelMessageSendComplex(thread.ID, &discordgo.MessageSend{
		Flags:      discordgo.MessageFlagsIsComponentsV2,
		Components: autoIntroComment.components(),
	})
	if err != nil {
		s.deps.Config.Logger.Warnf("Failed to post auto-post to intro thread %s: %v", thread.ID, err)
		// Don't fail the overall function; feed post was successful
	}
}

// BumpIntroToFeed manually bumps an intro thread to the feed channel.
// Unlike automatic forwarding, this returns an error/message to show the user.
// If skipEligibilityCheck is true, bypasses the cooldown check (for moderators).
func (s *IntroFeedService) BumpIntroToFeed(guildID, threadID, userID, displayName, threadName string, skipEligibilityCheck bool) error {
	// Check eligibility unless bypassed
	if !skipEligibilityCheck {
		eligibility, err := s.CheckFeedEligibility(guildID, userID)
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

// GetUserLatestIntroContent returns the user's latest intro thread metadata along
// with the text body of its starter message. For Discord forum posts the starter
// message ID equals the thread ID, so the body is ChannelMessage(threadID, threadID).
// Returns (nil, "", nil) when the user has no intro thread, and a non-nil error when
// the thread exists but its starter message cannot be read.
func (s *IntroFeedService) GetUserLatestIntroContent(userID string) (*forumcache.ThreadMeta, string, error) {
	meta, ok := s.GetUserLatestIntroThread(userID)
	if !ok || meta == nil {
		return nil, "", nil
	}
	if s.deps.Session == nil {
		return meta, "", fmt.Errorf("discord session not available")
	}
	msg, err := s.deps.Session.ChannelMessage(meta.ID, meta.ID)
	if err != nil {
		return meta, "", fmt.Errorf("failed to fetch intro starter message: %w", err)
	}
	if msg == nil {
		return meta, "", fmt.Errorf("intro starter message not found")
	}
	return meta, msg.Content, nil
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
		if slices.Contains(member.Roles, roleID) {
			return region
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

func (s *IntroFeedService) ScheduledFuncs() map[string]func() error {
	return map[string]func() error{
		"@hourly": s.reconcileIntroAvailableRole,
	}
}

// Checks all members of the guild and ensures that those who are eligible for the
// intro_available role have it, and those who are not eligible do not have it.
func (s *IntroFeedService) reconcileIntroAvailableRole() error {
	if s.deps.Session == nil {
		return nil
	}

	guildID := s.deps.Config.GetGamerPalsServerID()
	if guildID == "" {
		s.deps.Config.Logger.Warn("[IntroAvailable] Skipping reconciliation: guild ID not configured")
		return nil
	}

	roleID := s.deps.Config.ForGuild(guildID).GetIntroAvailableRoleID()
	if roleID == "" {
		s.deps.Config.Logger.Warnf("[IntroAvailable] Skipping reconciliation for guild %s: intro_available_role_id is not configured", guildID)
		return nil
	}
	if s.deps.ForumCache == nil {
		s.deps.Config.Logger.Warnf("[IntroAvailable] Skipping reconciliation for guild %s: forum cache is unavailable", guildID)
		return nil
	}

	members, err := utils.GetAllHumanGuildMembers(s.deps.Session, guildID)
	if err != nil {
		return fmt.Errorf("failed to fetch guild members for intro-available reconciliation: %w", err)
	}

	now := time.Now()
	var scanned, eligible, added, skippedExisting, skippedNotEligible, failures int
	for _, member := range members {
		if member == nil || member.User == nil {
			continue
		}
		scanned++
		userID := member.User.ID

		if slices.Contains(member.Roles, roleID) {
			skippedExisting++
			continue
		}

		latestMeta, _ := s.GetUserLatestIntroThread(userID)
		if !s.checkIntroRoleEligibility(member, latestMeta, now) {
			skippedNotEligible++
			continue
		}
		eligible++

		if err := s.deps.Session.GuildMemberRoleAdd(guildID, userID, roleID); err != nil {
			s.deps.Config.Logger.Warnf("[IntroAvailable] Failed to add role %s to user %s in guild %s: %v", roleID, userID, guildID, err)
			failures++
			continue
		}
		added++
	}

	s.deps.Config.Logger.Infof(
		"[IntroAvailable] Reconciliation complete (guild=%s): scanned=%d eligible=%d added=%d skipped_existing=%d skipped_not_eligible=%d failures=%d",
		guildID, scanned, eligible, added, skippedExisting, skippedNotEligible, failures,
	)
	return nil
}

// Checks if the configured cooldown period has passed since the user's last intro post.
func (s *IntroFeedService) checkIntroRoleEligibility(member *discordgo.Member, latestIntro *forumcache.ThreadMeta, now time.Time) bool {
	if latestIntro == nil || latestIntro.CreatedAt.IsZero() {
		return true
	}
	cooldownHours := s.cooldownHoursForMember(member)
	eligibleAt := latestIntro.CreatedAt.Add(time.Duration(cooldownHours) * time.Hour)
	return !now.Before(eligibleAt)
}

func (s *IntroFeedService) removeIntroAvailableRoleIfPresent(guildID, userID string) error {
	if s.deps.Session == nil {
		return nil
	}
	roleID := s.deps.Config.ForGuild(guildID).GetIntroAvailableRoleID()
	if roleID == "" {
		return nil
	}

	member, err := s.deps.Session.GuildMember(guildID, userID)
	if err != nil {
		s.deps.Config.Logger.Warnf("[IntroAvailable] Failed to fetch member %s in guild %s for role removal: %v", userID, guildID, err)
		return err
	}
	if member == nil || !slices.Contains(member.Roles, roleID) {
		return nil
	}

	err = s.deps.Session.GuildMemberRoleRemove(guildID, userID, roleID)
	if err != nil {
		s.deps.Config.Logger.Warnf("[IntroAvailable] Failed to remove role %s from user %s in guild %s: %v", roleID, userID, guildID, err)
	}
	return err
}
