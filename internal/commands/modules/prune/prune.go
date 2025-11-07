package prune

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"sort"
	"strconv"
	"time"

	"gamerpal/internal/forumcache"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// flaggedPost represents a thread we identified during prune-forum operations.
// "Flagged" threads have a concrete reason and may be deleted in execute mode.
// "Unknown" threads exceeded heuristics (e.g. too long) and need manual review.
// We store: thread channel reference, the reason string, the owner ID, and the
// creation timestamp (snowflake derived or cache metadata) for downstream audit/CSV.
type flaggedPost struct {
	thread    *discordgo.Channel
	reason    string
	ownerID   string
	createdAt time.Time
}

const maxInactiveUsersDisplay = 20

// handlePruneInactive handles the prune-inactive slash command
func (m *PruneModule) handlePruneInactive(s *discordgo.Session, i *discordgo.InteractionCreate) {
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
func (m *PruneModule) handlePruneForum(s *discordgo.Session, i *discordgo.InteractionCreate) {
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
	duplicatesCleanup := false
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
		if opt.Name == "duplicates_cleanup" {
			duplicatesCleanup = opt.BoolValue()
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

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource})

	threads, err := getAllActiveThreads(s, forumID, i.GuildID)
	if err != nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr(fmt.Sprintf("❌ Error accessing forum: %v", err))})
		return
	}

	// Build a set of moderator user IDs (simple rule: has Ban Members permission in forum channel => moderator)
	moderatorIDs := make(map[string]struct{})
	guildMembers, gmErr := utils.GetAllGuildMembers(s, i.GuildID)
	if gmErr != nil {
		m.config.Logger.Warnf("Failed to enumerate guild members for moderator detection: %v", gmErr)
	} else {
		for _, mbr := range guildMembers {
			perms, pErr := s.UserChannelPermissions(mbr.User.ID, forumID)
			if pErr != nil {
				continue
			}
			if perms&discordgo.PermissionBanMembers != 0 { // treat as moderator
				moderatorIDs[mbr.User.ID] = struct{}{}
			}
		}
	}

	var flaggedThreads []flaggedPost
	var unknownThreads []flaggedPost

	if duplicatesCleanup {
		if m.forumCache == nil {
			_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("❌ Forum cache unavailable; cannot run duplicates cleanup.")})
			return
		}
		m.forumCache.RegisterForum(forumID)
		cachedThreads, ok := m.forumCache.ListThreads(forumID)
		if !ok || len(cachedThreads) == 0 {
			_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr("❌ Forum cache not populated for this forum; run a refresh first.")})
			return
		}
		members, memErr := utils.GetAllGuildMembers(s, i.GuildID)
		if memErr != nil {
			_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr(fmt.Sprintf("❌ Error fetching guild members for departure check: %v", memErr))})
			return
		}
		memberSet := make(map[string]struct{}, len(members))
		for _, mbr := range members {
			memberSet[mbr.User.ID] = struct{}{}
		}
		byOwner := make(map[string][]*forumcache.ThreadMeta)
		for _, tm := range cachedThreads {
			byOwner[tm.OwnerID] = append(byOwner[tm.OwnerID], tm)
		}
		for ownerID, metas := range byOwner {
			// Skip moderators entirely (never flag duplicates and never treat departure as a reason)
			if _, isMod := moderatorIDs[ownerID]; isMod {
				continue
			}
			if _, ok := memberSet[ownerID]; !ok { // departed owner: flag all threads for audit
				for _, meta := range metas {
					flaggedThreads = append(flaggedThreads, flaggedPost{
						thread:    &discordgo.Channel{ID: meta.ID, ParentID: forumID},
						reason:    "owner departed",
						ownerID:   ownerID,
						createdAt: meta.CreatedAt,
					})
				}
				continue
			}
			if len(metas) <= 1 { // single thread => nothing to dedupe
				continue
			}
			// Sort threads oldest -> newest using CreatedAt then ID for deterministic ordering.
			// We preserve the newest thread (last element) and flag all older ones as duplicates.
			sort.Slice(metas, func(i, j int) bool {
				if metas[i].CreatedAt.Equal(metas[j].CreatedAt) {
					return metas[i].ID < metas[j].ID // tie-break: lower snowflake treated as older
				}
				return metas[i].CreatedAt.Before(metas[j].CreatedAt)
			})
			for _, meta := range metas[:len(metas)-1] { // all but newest
				flaggedThreads = append(flaggedThreads, flaggedPost{
					thread:    &discordgo.Channel{ID: meta.ID, ParentID: forumID},
					reason:    "duplicate (older thread)",
					ownerID:   ownerID,
					createdAt: meta.CreatedAt,
				})
			}
		}
	} else {
		for _, th := range threads {
			if !th.IsThread() {
				continue
			}
			// Skip if thread owner is a moderator
			if _, isMod := moderatorIDs[th.OwnerID]; isMod {
				continue
			}
			maxMessages := 500
			msgs, err := fetchMessagesLimited(s, th.ID, maxMessages)
			if err != nil {
				m.config.Logger.Warnf("Error fetching messages for thread %s: %v", th.ID, err)
				continue
			}
			if len(msgs) == 0 {
				created, _ := snowflakeTime(th.ID)
				flaggedThreads = append(flaggedThreads, flaggedPost{
					thread:    th,
					reason:    "no messages remain",
					ownerID:   th.OwnerID,
					createdAt: created,
				})
				continue
			}
			threadCreated, _ := snowflakeTime(th.ID)
			hasOwnerMessage := false
			for _, msg := range msgs {
				if msg.Author != nil && msg.Author.ID == th.OwnerID {
					hasOwnerMessage = true
					break
				}
			}
			// Original starter-missing heuristic block:
			// Only execute deep checks if the thread has a "reasonable" number of messages.
			// (Discord's MessageCount may cap at 50, but this heuristic remains as an upper guard.)
			// We detect extremely long threads early and push them into the "unknown" bucket to avoid
			// expensive pagination beyond our chosen limit.
			if forumChannel.MessageCount <= maxMessages {
				oldest := msgs[len(msgs)-1]
				if hasMore, _ := hasMoreMessages(s, th.ID, oldest.ID); hasMore {
					unknownThreads = append(unknownThreads, flaggedPost{
						thread:    th,
						reason:    "Thread too long",
						ownerID:   th.OwnerID,
						createdAt: threadCreated,
					})
					continue
				}
				if oldest.Author == nil || oldest.Author.ID != th.OwnerID {
					flaggedThreads = append(flaggedThreads, flaggedPost{
						thread:    th,
						reason:    "starter missing (oldest message not by owner)",
						ownerID:   th.OwnerID,
						createdAt: threadCreated,
					})
					continue
				}
				if !threadCreated.IsZero() {
					gap := oldest.Timestamp.Sub(threadCreated)
					if gap > 2*time.Second {
						flaggedThreads = append(flaggedThreads, flaggedPost{
							thread:    th,
							reason:    fmt.Sprintf("starter missing (creation gap %s)", gap.Truncate(time.Second)),
							ownerID:   th.OwnerID,
							createdAt: threadCreated,
						})
						continue
					}
				}
				if !hasOwnerMessage {
					flaggedThreads = append(flaggedThreads, flaggedPost{
						thread:    th,
						reason:    "owner has no messages in thread",
						ownerID:   th.OwnerID,
						createdAt: threadCreated,
					})
				}
			} else {
				unknownThreads = append(unknownThreads, flaggedPost{
					thread:    th,
					reason:    "Thread too long",
					ownerID:   th.OwnerID,
					createdAt: threadCreated,
				})
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	sort.Slice(flaggedThreads, func(a, b int) bool { return flaggedThreads[a].thread.ID > flaggedThreads[b].thread.ID })

	deletedCount := 0
	failedCount := 0
	if execute {
		for _, f := range flaggedThreads {
			if _, err := s.ChannelDelete(f.thread.ID); err != nil {
				failedCount++
				m.config.Logger.Warnf("Failed deleting thread %s: %v", f.thread.ID, err)
				continue
			}
			deletedCount++
			time.Sleep(150 * time.Millisecond)
		}
	}

	mode := "Dry Run"
	if duplicatesCleanup {
		mode = "Duplicates Cleanup " + mode
	}
	color := utils.Colors.Info()
	if execute {
		if duplicatesCleanup {
			mode = "Duplicates Cleanup Executed"
		} else {
			mode = "Executed"
		}
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
	moderatorSkipped := 0
	if len(moderatorIDs) > 0 {
		for _, t := range threads {
			if _, ok := moderatorIDs[t.OwnerID]; ok {
				moderatorSkipped++
			}
		}
	}
	if len(flaggedThreads) > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Flagged Threads", Value: flaggedFieldValue})
	}
	if len(unknownThreads) > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Unknown Threads", Value: unknownFieldValue})
	}
	if moderatorSkipped > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Moderator Threads Skipped", Value: fmt.Sprintf("%d", moderatorSkipped)})
	}

	title := "Forum Prune Report"
	if duplicatesCleanup {
		title = "Forum Prune Report (Duplicates Cleanup)"
	}
	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Color:       color,
		Fields:      fields,
		Footer: &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Use /prune-forum forum:<#%s> %sexecute:true to delete flagged threads", forumID, func() string {
			if duplicatesCleanup {
				return "duplicates_cleanup:true "
			}
			return ""
		}())},
	}

	csvBytes, csvErr := buildForumPruneCSV(flaggedThreads, unknownThreads, duplicatesCleanup, i.GuildID)
	files := []*discordgo.File{}
	if csvErr != nil {
		m.config.Logger.Warnf("Failed to build CSV for prune-forum: %v", csvErr)
		embed.Footer.Text += " • CSV generation failed"
	} else {
		files = append(files, &discordgo.File{Name: "forum_prune_report.csv", ContentType: "text/csv", Reader: bytes.NewReader(csvBytes)})
	}

	_, err = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}, Files: files})
	if err != nil {
		m.config.Logger.Errorf("Error sending prune-forum response: %v", err)
	}
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

// buildForumPruneCSV exports the full set of flagged and unknown threads to CSV.
// Columns: thread_id, status (flagged|unknown), reason, owner_id, created_at_iso, mode
func buildForumPruneCSV(flaggedThreads []flaggedPost, unknownThreads []flaggedPost, duplicatesMode bool, guildID string) ([]byte, error) {
	buf := &bytes.Buffer{}
	w := csv.NewWriter(buf)
	mode := "starter_missing"
	if duplicatesMode {
		mode = "duplicates_cleanup"
	}
	if err := w.Write([]string{"thread_id", "status", "reason", "owner_id", "created_at_iso", "mode", "url"}); err != nil { // header
		return nil, err
	}
	for _, f := range flaggedThreads {
		created := ""
		if !f.createdAt.IsZero() {
			created = f.createdAt.UTC().Format(time.RFC3339)
		}
		url := fmt.Sprintf("https://discord.com/channels/%s/%s", guildID, f.thread.ID)
		if err := w.Write([]string{f.thread.ID, "flagged", f.reason, f.ownerID, created, mode, url}); err != nil {
			return nil, err
		}
	}
	for _, f := range unknownThreads {
		created := ""
		if !f.createdAt.IsZero() {
			created = f.createdAt.UTC().Format(time.RFC3339)
		}
		url := fmt.Sprintf("https://discord.com/channels/%s/%s", guildID, f.thread.ID)
		if err := w.Write([]string{f.thread.ID, "unknown", f.reason, f.ownerID, created, mode, url}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
