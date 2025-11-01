package intro

import (
	"fmt"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/utils"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Test hook variables (overridable in tests). Defaults call discordgo / utils directly.
var (
	introRespond = func(s *discordgo.Session, inter *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
		return s.InteractionRespond(inter, resp)
	}
	introEdit = func(s *discordgo.Session, inter *discordgo.Interaction, edit *discordgo.WebhookEdit) (*discordgo.Message, error) {
		return s.InteractionResponseEdit(inter, edit)
	}
	introLog = func(cfg *types.Dependencies, s *discordgo.Session, msg string) error {
		return utils.LogToChannel(cfg.Config, s, msg)
	}
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
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
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

	_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Cache-only lookup path (no API fallback). On miss we log and return not-found.
	if m.config.ForumCache != nil {
		if meta, ok := m.config.ForumCache.GetLatestUserThread(introsChannelID, targetUser.ID); ok && meta != nil {
			postURL := fmt.Sprintf("https://discord.com/channels/%s/%s", i.GuildID, meta.ID)
			_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{
				Content: utils.StringPtr(postURL),
			})
			return
		}
		// Miss: log stats (no refresh)
		stats, ok := m.config.ForumCache.Stats(introsChannelID)
		var statsLine string
		if ok {
			statsLine = fmt.Sprintf("threads=%d owners=%d last_full_sync=%s adds=%d updates=%d deletes=%d anomalies=%d", stats.Threads, stats.OwnersTracked, stats.LastFullSync.Format(time.RFC3339), stats.EventAdds, stats.EventUpdates, stats.EventDeletes, stats.Anomalies)
		} else {
			statsLine = "stats_unavailable"
		}
		logMsg := fmt.Sprintf("[IntroCacheMiss] user=%s (%s) forum=%s guild=%s %s", targetUser.String(), targetUser.ID, introsChannelID, i.GuildID, statsLine)
		if err := introLog(m.config, s, logMsg); err != nil {
			m.config.Config.Logger.Warnf("failed to log intro cache miss: %v", err)
		}
	}

	_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr(fmt.Sprintf("❌ No introduction post found for %s.", targetUser.Mention())),
	})
}

// getAllActiveThreads gets all active threads from a forum channel

// Service returns nil as this module has no services requiring initialization
func (m *IntroModule) Service() types.ModuleService {
	return nil
}
