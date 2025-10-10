package userstats

import (
	"fmt"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/utils"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface for the userstats command
type UserstatsModule struct{}

// New creates a new userstats module
func New() *UserstatsModule {
	return &UserstatsModule{}
}

// Register adds the userstats command to the command map
func (m *UserstatsModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	var modPerms int64 = discordgo.PermissionBanMembers

	cmds["userstats"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "userstats",
			Description:              "Show member statistics for the server",
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			DefaultMemberPermissions: &modPerms,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "stats",
					Description: "Which statistics to show",
					Required:    false,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{
							Name:  "Overview",
							Value: "overview",
						},
						{
							Name:  "Daily (Last 7 Days)",
							Value: "daily",
						},
					},
				},
			},
		},
		HandlerFunc: m.handleUserStats,
	}
}

// handleUserStats handles the usercount slash command
func (m *UserstatsModule) handleUserStats(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Acknowledge the interaction immediately
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Get the stats type from the option (default to overview)
	statsType := "overview"
	if len(i.ApplicationCommandData().Options) > 0 && i.ApplicationCommandData().Options[0].Name == "stats" {
		statsType = i.ApplicationCommandData().Options[0].StringValue()
	}

	// Get guild members
	members, err := utils.GetAllGuildMembers(s, i.GuildID)
	if err != nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ Error fetching server members: " + err.Error()),
		})
		return
	}

	switch statsType {
	case "daily":
		m.handleDailyStats(s, i, members)
	default:
		m.handleOverviewStats(s, i, members)
	}
}

// handleOverviewStats handles the overview statistics display
func (m *UserstatsModule) handleOverviewStats(s *discordgo.Session, i *discordgo.InteractionCreate, members []*discordgo.Member) {
	// Count user types
	userCount := 0
	botCount := 0

	for _, member := range members {
		if member.User.Bot {
			botCount++
		} else {
			userCount++
		}
	}

	// Count number of members that joined < 30 days ago, < 180 days ago,
	// and < 365 days ago
	var joined30DaysAgoCount int
	var joined180DaysAgoCount int
	var joined365DaysAgoCount int

	// Calculate growth percentages
	var growth30Days float64
	var growth180Days float64
	var growth365Days float64

	for _, member := range members {
		if member.User.Bot {
			continue // Skip bots
		}

		joinedAt := member.JoinedAt
		daysAgo := int(time.Since(joinedAt).Hours() / 24)

		switch {
		case daysAgo <= 30:
			joined30DaysAgoCount++
			fallthrough
		case daysAgo <= 180:
			joined180DaysAgoCount++
			fallthrough
		case daysAgo <= 365:
			joined365DaysAgoCount++
		}
	}

	// Calculate growth percentages
	members30DaysAgo := userCount - joined30DaysAgoCount
	members180DaysAgo := userCount - joined180DaysAgoCount
	members365DaysAgo := userCount - joined365DaysAgoCount

	if members30DaysAgo > 0 {
		growth30Days = (float64(joined30DaysAgoCount) / float64(members30DaysAgo)) * 100
	}

	if members180DaysAgo > 0 {
		growth180Days = (float64(joined180DaysAgoCount) / float64(members180DaysAgo)) * 100
	}

	if members365DaysAgo > 0 {
		growth365Days = (float64(joined365DaysAgoCount) / float64(members365DaysAgo)) * 100
	}

	// Breakdown by region roles
	regions := map[string]string{
		"NA": "475040060786343937",
		"EU": "475039994554351618",
		"SA": "475040095993593866",
		"AS": "475040122463846422",
		"OC": "505413573586059266",
		"ZA": "518493780308000779",
	}

	// Count members by region roles
	regionCounts := make(map[string]int)
	for _, member := range members {
		if member.User.Bot {
			continue // Skip bots
		}

		for region, roleID := range regions {
			for _, role := range member.Roles {
				if role == roleID {
					regionCounts[region]++
					break // Found the region, no need to check others
				}
			}
		}
	}

	// Create embed response
	embed := &discordgo.MessageEmbed{
		Title: "📊 Server Statistics - Overview",
		Color: utils.Colors.Info(),
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "👥 Users",
				Value:  fmt.Sprintf("%d", userCount),
				Inline: true,
			},
			{
				Name:   "🤖 Bots",
				Value:  fmt.Sprintf("%d", botCount),
				Inline: true,
			},
			{
				Name:   "📈 Total Members",
				Value:  fmt.Sprintf("%d", userCount+botCount),
				Inline: true,
			},
			{
				Name:   "📅 Joined in the Last 30 Days",
				Value:  fmt.Sprintf("%d", joined30DaysAgoCount),
				Inline: true,
			},
			{
				Name:   "📅 Joined in the Last 180 Days",
				Value:  fmt.Sprintf("%d", joined180DaysAgoCount),
				Inline: true,
			},
			{
				Name:   "📅 Joined in the Last 365 Days",
				Value:  fmt.Sprintf("%d", joined365DaysAgoCount),
				Inline: true,
			},
			{
				Name:   "📈 Growth in the Last 30 Days",
				Value:  fmt.Sprintf("%.2f%%", growth30Days),
				Inline: true,
			},
			{
				Name:   "📈 Growth in the Last 180 Days",
				Value:  fmt.Sprintf("%.2f%%", growth180Days),
				Inline: true,
			},
			{
				Name:   "📈 Growth in the Last 365 Days",
				Value:  fmt.Sprintf("%.2f%%", growth365Days),
				Inline: true,
			},
			{
				Name:   "🌍 Members by Region",
				Value:  formatRegionCounts(regionCounts),
				Inline: true,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "GamerPal Bot",
		},
	}

	// Send the response
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	})
}

// handleDailyStats handles the daily statistics display for the last 7 days
func (m *UserstatsModule) handleDailyStats(s *discordgo.Session, i *discordgo.InteractionCreate, members []*discordgo.Member) {
	// Create a map to count joins by day for the last 7 days
	dailyCounts := make(map[string]int)
	now := time.Now()

	// Initialize the last 7 days with 0 counts
	for i := 0; i < 7; i++ {
		day := now.AddDate(0, 0, -i)
		dayKey := day.Format("2006-01-02")
		dailyCounts[dayKey] = 0
	}

	// Count joins by day for non-bot members
	for _, member := range members {
		if member.User.Bot {
			continue // Skip bots
		}

		joinedAt := member.JoinedAt
		daysSince := int(time.Since(joinedAt).Hours() / 24)

		// Only count if joined within the last 7 days
		if daysSince < 7 {
			dayKey := joinedAt.Format("2006-01-02")
			if _, exists := dailyCounts[dayKey]; exists {
				dailyCounts[dayKey]++
			}
		}
	}

	// Build the embed fields for each day
	var fields []*discordgo.MessageEmbedField
	totalWeekJoins := 0

	for i := 6; i >= 0; i-- { // Show from oldest to newest
		day := now.AddDate(0, 0, -i)
		dayKey := day.Format("2006-01-02")
		count := dailyCounts[dayKey]
		totalWeekJoins += count

		// Format the day display
		var dayDisplay string
		switch i {
		case 0:
			dayDisplay = "Today"
		case 1:
			dayDisplay = "Yesterday"
		default:
			dayDisplay = day.Format("Mon, Jan 2")
		}

		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("📅 %s", dayDisplay),
			Value:  fmt.Sprintf("%d new members", count),
			Inline: true,
		})
	}

	// Add a summary field
	fields = append(fields, &discordgo.MessageEmbedField{
		Name:   "📊 Weekly Summary",
		Value:  fmt.Sprintf("**%d** total new members this week", totalWeekJoins),
		Inline: false,
	})

	embed := &discordgo.MessageEmbed{
		Title:       "📊 Server Statistics - Daily Joins (Last 7 Days)",
		Color:       utils.Colors.Info(),
		Description: "New member joins by day for the past week",
		Fields:      fields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: "GamerPal Bot",
		},
	}

	// Send the response
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	})
}

func formatRegionCounts(regionCounts map[string]int) string {
	if len(regionCounts) == 0 {
		return "No members found in any region."
	}

	result := ""
	for region, count := range regionCounts {
		result += fmt.Sprintf("%s: %d\n", region, count)
	}
	return result
}

// GetServices returns nil as this module has no services requiring initialization
func (m *UserstatsModule) GetServices() []types.ModuleService {
return nil
}
