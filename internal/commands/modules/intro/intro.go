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

	// Slash command version
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
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "ephemeral",
					Description: "Whether the reply should be ephemeral (default: true)",
					Required:    false,
				},
			},
		},
		HandlerFunc: m.handleIntroSlash,
	}

	// User context (right-click / tap user) command version – enables quick lookup without typing.
	// For user & message context commands Discord allows spaces and capitalization.
	cmds["Lookup intro"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name: "Lookup intro",
			Type: discordgo.UserApplicationCommand,
		},
		HandlerFunc: m.handleIntroUserContext,
	}
}

// introLookup performs the introduction post lookup for the specified target user,
// and responds to the interaction accordingly.
func (m *IntroModule) introLookup(s *discordgo.Session, i *discordgo.InteractionCreate, targetUser *discordgo.User, ephemeral bool) {
	introsChannelID := m.config.Config.GetGamerPalsIntroductionsForumChannelID()
	if introsChannelID == "" {
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Introductions forum channel is not configured.",
				Flags:   chooseEphemeralFlag(ephemeral),
			},
		})
		return
	}

	_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: chooseEphemeralFlag(ephemeral),
		},
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

// Slash command handler – determines target from optional "user" option.
func (m *IntroModule) handleIntroSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	var targetUser *discordgo.User
	options := i.ApplicationCommandData().Options
	ephemeral := true // default
	for _, opt := range options {
		if opt.Name == "user" {
			targetUser = opt.UserValue(s)
		}
		if opt.Name == "ephemeral" {
			ephemeral = opt.BoolValue()
		}
	}
	if targetUser == nil && i.Member != nil {
		targetUser = i.Member.User
	}
	if targetUser == nil {
		// Fallback – shouldn't occur for slash commands, but handle defensively.
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Unable to resolve target user.",
				Flags:   chooseEphemeralFlag(ephemeral),
			},
		})
		return
	}
	m.introLookup(s, i, targetUser, ephemeral)
}

// User context command handler – target user resolved from interaction TargetID.
func (m *IntroModule) handleIntroUserContext(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	targetID := data.TargetID
	var targetUser *discordgo.User
	if data.Resolved != nil && data.Resolved.Users != nil {
		targetUser = data.Resolved.Users[targetID]
	}
	if targetUser == nil {
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Unable to resolve selected user.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}
	// User context command is always ephemeral per requirements.
	m.introLookup(s, i, targetUser, true)
}

// chooseEphemeralFlag returns the ephemeral flag if true, else 0.
func chooseEphemeralFlag(ephemeral bool) discordgo.MessageFlags {
	if ephemeral {
		return discordgo.MessageFlagsEphemeral
	}
	return 0
}

// Service returns nil as this module has no services requiring initialization
func (m *IntroModule) Service() types.ModuleService {
	return nil
}
