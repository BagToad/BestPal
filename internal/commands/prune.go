package commands

import (
	"fmt"
	"gamerpal/internal/utils"
	"log"

	"github.com/bwmarrin/discordgo"
)

const maxInactiveUsersDisplay = 20

// handlePruneInactive handles the prune-inactive slash command
func (h *Handler) handlePruneInactive(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Check if user has administrator permissions
	if !utils.HasAdminPermissions(s, i) {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
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
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Get all guild members
	members, err := utils.GetAllGuildMembers(s, i.GuildID)
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
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
		color = 0xff6b6b // Red color for execution

		for _, member := range usersWithoutRoles {
			err := s.GuildMemberDeleteWithReason(i.GuildID, member.User.ID, "Pruned: User is inactive")
			if err != nil {
				log.Printf("Error removing user %s: %v", member.User.Username, err)
			} else {
				removedCount++
				log.Printf("Removed user: %s#%s", member.User.Username, member.User.Discriminator)
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
		color = 0x4ecdc4 // Teal color for dry run
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
	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	})
}
