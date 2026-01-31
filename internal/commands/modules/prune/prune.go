package prune

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"time"

	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

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

// handlePruneForum scans a forum channel for threads from departed owners and duplicate intros.
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
	execute := false
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "forum" {
			forumChannel := opt.ChannelValue(s)
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

	// Run the shared prune logic
	result, err := RunIntroPrune(s, m.config, m.forumCache, forumID, i.GuildID, !execute)
	if err != nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: utils.StringPtr(fmt.Sprintf("❌ Error: %v", err))})
		return
	}

	// Build response
	mode := "Dry Run"
	color := utils.Colors.Info()
	if execute {
		mode = "Executed"
		color = utils.Colors.Warning()
	}

	description := fmt.Sprintf("Mode: %s\nForum: <#%s>\nThreads scanned: %d\nThreads flagged: %d\nModerator threads skipped: %d",
		mode, forumID, result.ThreadsScanned, result.ThreadsFlagged, result.ModeratorSkipped)
	if execute {
		description += fmt.Sprintf("\nThreads deleted: %d\nDelete failures: %d", result.ThreadsDeleted, result.DeleteFailures)
	}

	maxList := 20
	flaggedFieldValue := ""
	for idx, f := range result.FlaggedThreads {
		if idx >= maxList {
			flaggedFieldValue += fmt.Sprintf("\n…and %d more", len(result.FlaggedThreads)-maxList)
			break
		}
		if f.Username != "" {
			flaggedFieldValue += fmt.Sprintf("• <#%s> by %s — %s\n", f.ThreadID, f.Username, f.Reason)
		} else {
			flaggedFieldValue += fmt.Sprintf("• <#%s> — %s\n", f.ThreadID, f.Reason)
		}
	}

	fields := []*discordgo.MessageEmbedField{}
	if len(result.FlaggedThreads) > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Flagged Threads", Value: flaggedFieldValue})
	}

	embed := &discordgo.MessageEmbed{
		Title:       "Forum Prune Report",
		Description: description,
		Color:       color,
		Fields:      fields,
		Footer:      &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Use /prune-forum forum:<#%s> execute:true to delete flagged threads", forumID)},
	}

	// Build CSV for download
	csvBytes, csvErr := buildForumPruneCSVFromResult(result, i.GuildID)
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

// buildForumPruneCSVFromResult exports FlaggedThread results to CSV.
// Columns: thread_id, status, reason, owner_id, username, created_at_iso, url
func buildForumPruneCSVFromResult(result *IntroPruneResult, guildID string) ([]byte, error) {
	buf := &bytes.Buffer{}
	w := csv.NewWriter(buf)
	if err := w.Write([]string{"thread_id", "status", "reason", "owner_id", "username", "created_at_iso", "url"}); err != nil {
		return nil, err
	}
	for _, f := range result.FlaggedThreads {
		created := ""
		if !f.CreatedAt.IsZero() {
			created = f.CreatedAt.UTC().Format(time.RFC3339)
		}
		url := fmt.Sprintf("https://discord.com/channels/%s/%s", guildID, f.ThreadID)
		if err := w.Write([]string{f.ThreadID, "flagged", f.Reason, f.OwnerID, f.Username, created, url}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
