package commands

import (
	"fmt"
	"gamerpal/internal/utils"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// handleRouletteAdmin handles the roulette-admin command and its subcommands
func (h *SlashHandler) handleRouletteAdmin(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Check if user has admin permissions
	if !utils.HasAdminPermissions(s, i) {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ You need administrator permissions to use this command.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Get the subcommand
	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Please specify a subcommand. Use `/roulette-admin debug`, `/roulette-admin pair`, `/roulette-admin reset`, or `/roulette-admin delete-schedule`",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	subcommand := options[0]
	switch subcommand.Name {
	case "debug":
		h.handleRouletteAdminDebug(s, i)
	case "pair":
		h.handleRouletteAdminPair(s, i, subcommand.Options)
	case "reset":
		h.handleRouletteAdminReset(s, i)
	case "delete-schedule":
		h.handleRouletteAdminDeleteSchedule(s, i)
	case "simulate-pairing":
		h.handleRouletteAdminSimulatePairing(s, i, subcommand.Options)
	default:
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Unknown subcommand",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
	}
}

// handleRouletteAdminDebug shows debug information about the roulette system
func (h *SlashHandler) handleRouletteAdminDebug(s *discordgo.Session, i *discordgo.InteractionCreate) {
	guildID := i.GuildID

	// Defer response since this might take a moment
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	var response strings.Builder
	response.WriteString("🔧 **Roulette System Debug Information**\n\n")

	// Get scheduled pairing info
	schedule, err := h.DB.GetRouletteSchedule(guildID)
	if err != nil {
		response.WriteString(fmt.Sprintf("❌ Error getting schedule: %v\n\n", err))
	} else if schedule == nil {
		response.WriteString("📅 **Next Scheduled Pairing:** None scheduled\n\n")
	} else {
		response.WriteString(fmt.Sprintf("📅 **Next Scheduled Pairing:** <t:%d:F>\n\n", schedule.ScheduledAt.Unix()))
	}

	// Get signed up users
	signups, err := h.DB.GetRouletteSignups(guildID)
	if err != nil {
		response.WriteString(fmt.Sprintf("❌ Error getting signups: %v\n\n", err))
	} else {
		response.WriteString(fmt.Sprintf("👥 **Users Signed Up:** %d\n", len(signups)))
		if len(signups) > 0 {
			for _, signup := range signups {
				user, err := s.User(signup.UserID)
				username := signup.UserID
				if err == nil {
					username = user.Username
				}

				// Get user's games
				games, err := h.DB.GetRouletteGames(signup.UserID, guildID)

				var gameNames []string
				for _, game := range games {
					gameNames = append(gameNames, game.GameName)
				}
				gamesList := strings.Join(gameNames, ", ")

				response.WriteString(fmt.Sprintf("• %s (%s)\n", username, gamesList))
			}
		}
		response.WriteString("\n")
	}

	// Get config info
	pairingCategoryID := h.config.GetGamerPalsPairingCategoryID()
	modLogChannelID := h.config.GetGamerPalsModActionLogChannelID()

	response.WriteString("⚙️ **Configuration:**\n")
	response.WriteString(fmt.Sprintf("• Pairing Category ID: %s\n", pairingCategoryID))
	response.WriteString(fmt.Sprintf("• Mod Log Channel ID: %s\n", modLogChannelID))

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr(response.String()),
	})
}

// handleRouletteAdminPair handles the pairing command
func (h *SlashHandler) handleRouletteAdminPair(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	guildID := i.GuildID

	// Parse options
	var pairTime *time.Time
	immediatePair := false
	dryRun := true // Default to true

	for _, option := range options {
		switch option.Name {
		case "time":
			t, err := utils.ParseUnixTimestamp(option.StringValue())
			if err != nil {
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "❌ Unable to parse pair time.",
						Flags:   discordgo.MessageFlagsEphemeral,
					},
				})
				return
			}

			parsedTime := time.Unix(t, 0).UTC()
			pairTime = &parsedTime

		case "immediate-pair":
			immediatePair = option.BoolValue()
		case "dryrun":
			dryRun = option.BoolValue()
		}
	}

	// Defer response since pairing might take time
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	if immediatePair {
		// Execute pairing immediately
		h.executePairing(s, i, guildID, dryRun)
	} else if pairTime != nil {
		// Schedule pairing
		h.schedulePairing(s, i, guildID, *pairTime, dryRun)
	} else {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ You must specify either a pair time or set immediate-pair-bool to true."),
		})
	}
}

// schedulePairing schedules a pairing for later
func (h *SlashHandler) schedulePairing(s *discordgo.Session, i *discordgo.InteractionCreate, guildID string, pairTime time.Time, dryRun bool) {
	if !dryRun {
		err := h.DB.SetRouletteSchedule(guildID, pairTime)
		if err != nil {
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: utils.StringPtr(fmt.Sprintf("❌ Error scheduling pairing: %v", err)),
			})
			return
		}
	}

	dryRunText := ""
	if dryRun {
		dryRunText = " (DRY RUN - not actually scheduled)"
	}

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr(fmt.Sprintf("✅ Pairing scheduled for <t:%d:F>%s", pairTime.Unix(), dryRunText)),
	})
}

// executePairing executes the pairing algorithm
func (h *SlashHandler) executePairing(s *discordgo.Session, i *discordgo.InteractionCreate, guildID string, dryRun bool) {
	// Execute pairing using the pairing service
	result, err := h.PairingService.ExecutePairing(guildID, dryRun)
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr(fmt.Sprintf("❌ Error executing pairing: %v", err)),
		})
		return
	}

	// Build response
	var response strings.Builder
	dryRunText := ""
	if dryRun {
		dryRunText = " (DRY RUN)"
	}

	response.WriteString(fmt.Sprintf("🎰 **Roulette Pairing Results%s**\n\n", dryRunText))

	if !result.Success {
		response.WriteString(fmt.Sprintf("❌ %s\n", result.ErrorMessage))
	} else {
		response.WriteString(fmt.Sprintf("✅ Found %d pair(s):\n\n", result.PairCount))

		for i, pair := range result.Pairs {
			response.WriteString(fmt.Sprintf("**Pair %d:**\n", i+1))

			var commonGames []string
			// Find common games
			if len(pair) >= 2 {
				commonGames = h.PairingService.FindCommonGames(pair[0].Games, pair[1].Games)
				if len(pair) > 2 {
					// For groups of 3+, find games common to all
					for j := 2; j < len(pair); j++ {
						commonGames = h.PairingService.FindCommonGames(commonGames, pair[j].Games)
					}
				}
			}

			for _, user := range pair {
				response.WriteString(fmt.Sprintf("• <@%s>\n", user.UserID))
			}

			if len(commonGames) > 0 {
				response.WriteString(fmt.Sprintf("🎮 Common games: %s\n", strings.Join(commonGames, ", ")))
			} else {
				response.WriteString("🎮 No common games found\n")
			}

			response.WriteString("\n")
		}

		// List unpaired users
		if len(result.UnpairedUsers) > 0 {
			var unpairedMentions []string
			for _, userID := range result.UnpairedUsers {
				unpairedMentions = append(unpairedMentions, fmt.Sprintf("<@%s>", userID))
			}
			response.WriteString(fmt.Sprintf("⚠️ **Unpaired users (%d):** %s\n\n", len(result.UnpairedUsers), strings.Join(unpairedMentions, ", ")))
		}

		if !dryRun {
			// Log to mod action log
			h.PairingService.LogPairingResults(guildID, result, false)
		}
	}

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr(response.String()),
	})
}

// handleRouletteAdminReset handles the reset subcommand
func (h *SlashHandler) handleRouletteAdminReset(s *discordgo.Session, i *discordgo.InteractionCreate) {
	guildID := i.GuildID

	// Respond with thinking message
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	// Get count of existing pairing channels before cleanup
	pairingCategoryID := h.config.GetGamerPalsPairingCategoryID()
	channelCount := 0
	if pairingCategoryID != "" {
		channels, err := s.GuildChannels(guildID)
		if err == nil {
			for _, channel := range channels {
				if channel.ParentID == pairingCategoryID {
					channelCount++
				}
			}
		}
	}

	// Execute cleanup using the pairing service
	err := h.PairingService.CleanupPreviousPairings(guildID)

	var response strings.Builder
	if err != nil {
		response.WriteString(fmt.Sprintf("❌ **Reset Failed**\n\nError: %s", err.Error()))
	} else {
		response.WriteString(fmt.Sprintf("✅ **Pairing Channels Reset**\n\nDeleted %d pairing channels successfully.", channelCount))

		// Log the reset action to mod log
		modLogChannelID := h.config.GetGamerPalsModActionLogChannelID()
		if modLogChannelID != "" {
			logMessage := fmt.Sprintf("🔄 **Roulette Channels Reset**\n\n"+
				"**Administrator:** <@%s>\n"+
				"**Guild:** %s\n"+
				"**Channels Deleted:** %d\n"+
				"**Time:** <t:%d:F>",
				i.Member.User.ID, guildID, channelCount, time.Now().Unix())

			_, err := s.ChannelMessageSend(modLogChannelID, logMessage)
			if err != nil {
				h.config.Logger.Warn("Error sending reset notification: %v", err)
			}
		}
	}

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr(response.String()),
	})
}

// handleRouletteAdminDeleteSchedule handles removing a scheduled pairing
func (h *SlashHandler) handleRouletteAdminDeleteSchedule(s *discordgo.Session, i *discordgo.InteractionCreate) {
	guildID := i.GuildID

	// Defer response
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	// Check if there's a scheduled pairing first
	schedule, err := h.DB.GetRouletteSchedule(guildID)
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr(fmt.Sprintf("❌ Error checking schedule: %v", err)),
		})
		return
	}

	if schedule == nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ No scheduled pairing found to delete."),
		})
		return
	}

	// Store the schedule time for logging
	scheduledTime := schedule.ScheduledAt

	// Delete the schedule
	err = h.DB.ClearRouletteSchedule(guildID)
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr(fmt.Sprintf("❌ Error deleting schedule: %v", err)),
		})
		return
	}

	// Build success response
	response := fmt.Sprintf("✅ **Scheduled Pairing Deleted**\n\nThe pairing scheduled for <t:%d:F> has been removed.", scheduledTime.Unix())

	// Log the deletion to mod action log
	modLogChannelID := h.config.GetGamerPalsModActionLogChannelID()
	if modLogChannelID != "" {
		logMessage := fmt.Sprintf("🗑️ **Roulette Schedule Deleted**\n\n"+
			"**Administrator:** <@%s>\n"+
			"**Guild:** %s\n"+
			"**Scheduled Time:** <t:%d:F>\n"+
			"**Deletion Time:** <t:%d:F>",
			i.Member.User.ID, guildID, scheduledTime.Unix(), time.Now().Unix())

		_, err := s.ChannelMessageSend(modLogChannelID, logMessage)
		if err != nil {
			h.config.Logger.Warn("Error sending schedule deletion notification: %v", err)
		}
	}

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr(response),
	})
}

// handleRouletteAdminSimulatePairing handles the simulate-pairing subcommand
func (h *SlashHandler) handleRouletteAdminSimulatePairing(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	guildID := i.GuildID

	// Parse options
	userCount := 8          // Default to 8 users for simulation
	createChannels := false // Default to false

	for _, option := range options {
		switch option.Name {
		case "user-count":
			userCount = int(option.IntValue())
		case "create-channels":
			createChannels = option.BoolValue()
		}
	}

	// Validate user count
	if userCount < 4 {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ User count must be at least 4 for simulation.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if userCount > 50 {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ User count cannot exceed 50 for simulation.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Defer response since simulation might take time
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	// Execute simulation using the pairing service
	result, err := h.PairingService.SimulatePairing(guildID, userCount, createChannels)
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr(fmt.Sprintf("❌ Error executing simulation: %v", err)),
		})
		return
	}

	// Build response content for file
	var response strings.Builder
	channelText := ""
	if createChannels {
		channelText = " (Channels Created)"
	}

	response.WriteString(fmt.Sprintf("Roulette Pairing Simulation%s\n", channelText))
	response.WriteString(strings.Repeat("=", 50) + "\n\n")
	response.WriteString(fmt.Sprintf("Simulated Users: %d\n\n", userCount))

	if !result.Success {
		response.WriteString(fmt.Sprintf("ERROR: %s\n", result.ErrorMessage))
	} else {
		response.WriteString(fmt.Sprintf("SUCCESS: Found %d group(s)\n\n", result.PairCount))

		for i, pair := range result.Pairs {
			response.WriteString(fmt.Sprintf("Group %d:\n", i+1))
			response.WriteString(strings.Repeat("-", 20) + "\n")

			var commonGames []string
			// Find common games
			if len(pair) >= 2 {
				commonGames = h.PairingService.FindCommonGames(pair[0].Games, pair[1].Games)
				if len(pair) > 2 {
					// For groups of 3+, find games common to all
					for j := 2; j < len(pair); j++ {
						commonGames = h.PairingService.FindCommonGames(commonGames, pair[j].Games)
					}
				}
			}

			for _, user := range pair {
				// Show fake user info instead of mentioning real users
				response.WriteString(fmt.Sprintf("• %s\n", user.UserID))
				response.WriteString(fmt.Sprintf("  Games: %s\n", strings.Join(user.Games, ", ")))
				response.WriteString(fmt.Sprintf("  Regions: %s\n", strings.Join(user.Regions, ", ")))
			}

			if len(commonGames) > 0 {
				response.WriteString(fmt.Sprintf("Common games: %s\n", strings.Join(commonGames, ", ")))
			} else {
				response.WriteString("No common games found\n")
			}

			response.WriteString("\n")
		}

		// List unpaired users
		if len(result.UnpairedUsers) > 0 {
			response.WriteString(fmt.Sprintf("Unpaired users (%d):\n", len(result.UnpairedUsers)))
			response.WriteString(strings.Repeat("-", 20) + "\n")
			for _, userID := range result.UnpairedUsers {
				response.WriteString(fmt.Sprintf("• %s\n", userID))
			}
			response.WriteString("\n")
		}

		// Add efficiency stats
		pairedUsers := result.TotalUsers - len(result.UnpairedUsers)
		efficiency := float64(pairedUsers) / float64(result.TotalUsers) * 100
		response.WriteString(fmt.Sprintf("Pairing Efficiency: %.1f%% (%d/%d users paired)\n", efficiency, pairedUsers, result.TotalUsers))

		if createChannels {
			response.WriteString("\nNote: Simulation channels were created with fake users. Use /roulette-admin reset to clean them up.\n")
		}
	}

	// Create filename with timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("roulette_simulation_%s.txt", timestamp)

	// Send as file attachment
	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr(fmt.Sprintf("🧪 **Roulette Pairing Simulation Complete**\n\nResults saved to file. %d users simulated with %.1f%% pairing efficiency.",
			userCount,
			func() float64 {
				if result.Success {
					pairedUsers := result.TotalUsers - len(result.UnpairedUsers)
					return float64(pairedUsers) / float64(result.TotalUsers) * 100
				}
				return 0.0
			}())),
		Files: []*discordgo.File{
			{
				Name:   filename,
				Reader: strings.NewReader(response.String()),
			},
		},
	})
}
