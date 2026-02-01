package intro

import (
	"fmt"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/utils"
	"time"

	"github.com/MakeNowJust/heredoc"
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
	config      *types.Dependencies
	feedService *IntroFeedService
}

// New creates a new intro module
func New(deps *types.Dependencies) *IntroModule {
	return &IntroModule{
		feedService: NewIntroFeedService(deps),
	}
}

// GetFeedService returns the intro feed service for external access (e.g., event handlers)
func (m *IntroModule) GetFeedService() *IntroFeedService {
	return m.feedService
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

	// Bump intro command - manually post an intro to the feed channel
	cmds["bump-intro"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "bump-intro",
			Description: "Post your introduction to the introductions feed channel",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		},
		HandlerFunc: m.handleBumpIntro,
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

	// Resolve actor (the user performing the lookup) for logging purposes.
	var actor *discordgo.User
	if i.Member != nil && i.Member.User != nil {
		actor = i.Member.User
	} else if i.User != nil {
		actor = i.User
	}

	if introsChannelID == "" {
		failureMsg := heredoc.Doc(fmt.Sprintf(`
			[IntroLookupFailure]
			Reason: channel_not_configured
			Actor: %s (%s)
			Target: %s (%s)
			Guild: %s
			Ephemeral: %t
		`,
			func() string {
				if actor != nil {
					return actor.String()
				} else {
					return "<unknown>"
				}
			}(),
			func() string {
				if actor != nil {
					return actor.ID
				} else {
					return "<unknown>"
				}
			}(),
			targetUser.String(), targetUser.ID,
			i.GuildID,
			ephemeral,
		))
		if err := introLog(m.config, s, failureMsg); err != nil {
			m.config.Config.Logger.Warnf("failed to log intro lookup failure: %v", err)
		}
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
			// Success log
			successMsg := heredoc.Doc(fmt.Sprintf(`
				[IntroLookupSuccess]
				Actor: %s (%s)
				Target: %s (%s)
				Guild: %s
				Forum: %s
				Ephemeral: %t
				ThreadID: %s
				ThreadName: %s
				CreatedAt: %s
				URL: %s
			`,
				func() string {
					if actor != nil {
						return actor.String()
					} else {
						return "<unknown>"
					}
				}(),
				func() string {
					if actor != nil {
						return actor.ID
					} else {
						return "<unknown>"
					}
				}(),
				targetUser.String(), targetUser.ID,
				i.GuildID,
				introsChannelID,
				ephemeral,
				meta.ID,
				meta.Name,
				meta.CreatedAt.Format(time.RFC3339),
				postURL,
			))
			if err := introLog(m.config, s, successMsg); err != nil {
				m.config.Config.Logger.Warnf("failed to log intro success: %v", err)
			}
			_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{
				Content: utils.StringPtr(postURL),
			})
			return
		}

		// Failure (cache miss)
		stats, ok := m.config.ForumCache.Stats(introsChannelID)
		failureMsg := func() string {
			if ok {
				return heredoc.Doc(fmt.Sprintf(`
					[IntroLookupFailure]
					Reason: cache_miss
					Actor: %s (%s)
					Target: %s (%s)
					Guild: %s
					Forum: %s
					Ephemeral: %t
					Stats:
					  Threads: %d
					  OwnersTracked: %d
					  LastFullSync: %s
					  EventAdds: %d
					  EventUpdates: %d
					  EventDeletes: %d
					  Anomalies: %d
				`,
					func() string {
						if actor != nil {
							return actor.String()
						} else {
							return "<unknown>"
						}
					}(),
					func() string {
						if actor != nil {
							return actor.ID
						} else {
							return "<unknown>"
						}
					}(),
					targetUser.String(), targetUser.ID,
					i.GuildID,
					introsChannelID,
					ephemeral,
					stats.Threads,
					stats.OwnersTracked,
					stats.LastFullSync.Format(time.RFC3339),
					stats.EventAdds,
					stats.EventUpdates,
					stats.EventDeletes,
					stats.Anomalies,
				))
			}
			return heredoc.Doc(fmt.Sprintf(`
				[IntroLookupFailure]
				Reason: cache_miss
				Actor: %s (%s)
				Target: %s (%s)
				Guild: %s
				Forum: %s
				Ephemeral: %t
				Stats: unavailable
			`,
				func() string {
					if actor != nil {
						return actor.String()
					} else {
						return "<unknown>"
					}
				}(),
				func() string {
					if actor != nil {
						return actor.ID
					} else {
						return "<unknown>"
					}
				}(),
				targetUser.String(), targetUser.ID,
				i.GuildID,
				introsChannelID,
				ephemeral,
			))
		}()
		if err := introLog(m.config, s, failureMsg); err != nil {
			m.config.Config.Logger.Warnf("failed to log intro failure: %v", err)
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

// handleBumpIntro handles the /bump-intro command to manually post an intro to the feed
func (m *IntroModule) handleBumpIntro(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Get the user who invoked the command
	var user *discordgo.User
	if i.Member != nil && i.Member.User != nil {
		user = i.Member.User
	} else if i.User != nil {
		user = i.User
	}

	if user == nil {
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Unable to identify user.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Check if feed channel is configured
	feedChannelID := m.config.Config.GetIntroFeedChannelID()
	if feedChannelID == "" {
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Introduction feed channel is not configured.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Defer response as lookups might take a moment
	_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	// Look up user's latest intro
	if m.feedService == nil {
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ Service not available."),
		})
		return
	}

	meta, found := m.feedService.GetUserLatestIntroThread(user.ID)
	if !found || meta == nil {
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ You don't have an introduction post. Create one first!"),
		})
		return
	}

	// Get display name for the feed message
	displayName := user.DisplayName()
	if i.Member != nil && i.Member.Nick != "" {
		displayName = i.Member.Nick
	}

	// Check if user is a moderator (admin) - they can bypass the cooldown
	isModerator := false
	if i.Member != nil {
		isModerator = utils.HasAdminPermissions(s, i)
	}

	// Attempt to bump to feed
	err := m.feedService.BumpIntroToFeed(i.GuildID, meta.ID, user.ID, displayName, meta.Name, isModerator)
	if err != nil {
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr(fmt.Sprintf("❌ %s", err.Error())),
		})
		return
	}

	_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr("✅ Your introduction has been posted to the feed!"),
	})
}

// chooseEphemeralFlag returns the ephemeral flag if true, else 0.
func chooseEphemeralFlag(ephemeral bool) discordgo.MessageFlags {
	if ephemeral {
		return discordgo.MessageFlagsEphemeral
	}
	return 0
}

// Service returns nil as this module has no scheduled tasks or hydration needs
func (m *IntroModule) Service() types.ModuleService {
	return nil
}
