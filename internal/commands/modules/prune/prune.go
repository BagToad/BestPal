package prune

import (
	"fmt"
	"gamerpal/internal/utils"
	"sort"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
)

const maxInactiveUsersDisplay = 20

// handlePruneInactive handles the prune-inactive slash command
func (m *Module) handlePruneInactive(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Check if user has administrator permissions
	if !utils.HasAdminPermissions(s, i) {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ You need Administrator permissions to use this command.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Parse the execute option (defaults to false for dry run)
	execute := false
	if len(i.ApplicationCommandData().Options) > 0 {
		if i.ApplicationCommandData().Options[0].Name == "execute" {
			execute = i.ApplicationCommandData().Options[0].BoolValue()
		}
	}

	// Defer the response since this might take a while
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Get all guild members
	members, err := utils.GetAllGuildMembers(s, i.GuildID)
	if err != nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ Error fetching server members: " + err.Error()),
		})
		return
	}

	// Find users without roles (excluding bots)
	var usersWithoutRoles []*discordgo.Member
	for _, member := range members {
		if member.User.Bot {
			continue // Skip bots
		}

		// Check if user has any roles (besides @everyone which is implicit)
		if len(member.Roles) == 0 {
			usersWithoutRoles = append(usersWithoutRoles, member)
		}
	}

	// Prepare the response
	var title, description string
	var color int
	var removedCount int

	if execute {
		// Actually remove users
		title = "🔨 Prune Inactive Users - Execution"
		color = utils.Colors.Warning()

		for _, member := range usersWithoutRoles {
			err := s.GuildMemberDeleteWithReason(i.GuildID, member.User.ID, "Pruned: User is inactive")
			if err != nil {
				m.config.Logger.Warn("Error removing user %s: %v", member.User.Username, err)
			} else {
				removedCount++
				m.config.Logger.Infof("Removed user: %s#%s", member.User.Username, member.User.Discriminator)
			}
		}

		if removedCount > 0 {
			description = fmt.Sprintf("✅ Successfully removed %d users without roles.\n\n", removedCount)
		} else {
			description = "✅ No users were removed.\n\n"
		}
	} else {
		// Dry run
		title = "🔍 Prune Inactive Users - Dry Run"
		color = utils.Colors.Info()
		description = "This is a **dry run**. No users will be removed.\n\n"
	}

	// Add details about users found
	if len(usersWithoutRoles) > 0 {
		description += fmt.Sprintf("**Found %d users without roles:**\n", len(usersWithoutRoles))

		// Show up to maxDisplayUsers users in the response
		displayCount := len(usersWithoutRoles)
		if displayCount > maxInactiveUsersDisplay {
			displayCount = maxInactiveUsersDisplay
		}

		for i := 0; i < displayCount; i++ {
			member := usersWithoutRoles[i]
			description += fmt.Sprintf("• %s#%s", member.User.Username, member.User.Discriminator)
			if member.Nick != "" {
				description += fmt.Sprintf(" (Nick: %s)", member.Nick)
			}
			description += "\n"
		}

		if len(usersWithoutRoles) > 10 {
			description += fmt.Sprintf("... and %d more users\n", len(usersWithoutRoles)-10)
		}
	} else {
		description += "✅ No users without roles found!"
	}

	// Create embed response
	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Color:       color,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Total Members Checked",
				Value:  fmt.Sprintf("%d", len(members)),
				Inline: true,
			},
			{
				Name:   "Users Without Roles",
				Value:  fmt.Sprintf("%d", len(usersWithoutRoles)),
				Inline: true,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "GamerPal Bot • Use /prune-inactive execute:true to actually remove users",
		},
	}

	if execute && removedCount > 0 {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "Users Removed",
			Value:  fmt.Sprintf("%d", removedCount),
			Inline: true,
		})
	}

	// Send the response
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	})
}

// handlePruneForum scans a forum channel for threads whose starter was deleted.
// Dry run by default; when execute:true, deletes flagged threads.
func (m *Module) handlePruneForum(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Admin guard
	if !utils.HasAdminPermissions(s, i) {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
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
				_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
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
				_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
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
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ You must provide a forum channel to prune.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Defer response
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Fetch threads under the forum
	threads, err := getAllActiveThreads(s, forumID, i.GuildID)
	if err != nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
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
			m.config.Logger.Warnf("Error fetching messages for thread %s: %v", th.ID, err)
			continue
		}
		if len(msgs) == 0 {
			flaggedThreads = append(flaggedThreads, flagged{thread: th, reason: "no messages remain"})
			continue
		}

		threadCreated, err := snowflakeTime(th.ID)
		if err != nil {
			m.config.Logger.Warnf("Unable to parse snowflake for thread %s: %v", th.ID, err)
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
			// This should work though...
			if hasMore, _ := hasMoreMessages(s, th.ID, oldest.ID); hasMore {
				unknownThreads = append(unknownThreads, flagged{thread: th, reason: "Thread too long"})
				continue
			}
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
				m.config.Logger.Warnf("Failed deleting thread %s: %v", f.thread.ID, err)
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
	flaggedFieldValue := ""
	for idx, f := range flaggedThreads {
		if idx >= maxList {
			flaggedFieldValue += fmt.Sprintf("\n…and %d more", len(flaggedThreads)-maxList)
			break
		}
		flaggedFieldValue += fmt.Sprintf("• <#%s> — %s\n", f.thread.ID, f.reason)
	}

	unknownFieldValue := ""
	for idx, f := range unknownThreads {
		if idx >= maxList {
			unknownFieldValue += fmt.Sprintf("\n…and %d more", len(unknownThreads)-maxList)
			break
		}
		unknownFieldValue += fmt.Sprintf("• <#%s> — %s\n", f.thread.ID, f.reason)
	}

	fields := []*discordgo.MessageEmbedField{}
	if len(flaggedThreads) > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   "Flagged Threads",
			Value:  flaggedFieldValue,
			Inline: false,
		})
	}

	if len(unknownThreads) > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   "Unknown Threads",
			Value:  unknownFieldValue,
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

	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	})
}

// fetchMessagesLimited retrieves up to 'limitTotal' messages from a channel using pagination
func fetchMessagesLimited(s *discordgo.Session, channelID string, limitTotal int) ([]*discordgo.Message, error) {
	var all []*discordgo.Message
	var beforeID string
	const page = 100

	for limitTotal <= 0 || len(all) < limitTotal {
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

// hasMoreMessages checks if there are more messages in the channel before a given message ID
func hasMoreMessages(s *discordgo.Session, channelID string, beforeID string) (bool, error) {
	msgs, err := s.ChannelMessages(channelID, 1, beforeID, "", "")
	if err != nil {
		return false, err
	}
	return len(msgs) > 0, nil
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

// getAllActiveThreads gets all active threads from a forum channel
func getAllActiveThreads(s *discordgo.Session, channelID string, guildID string) ([]*discordgo.Channel, error) {
var allThreads []*discordgo.Channel

// For forum channels, get the channel and its threads directly
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
