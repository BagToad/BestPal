package fun

import (
	"fmt"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"math/rand"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc"
	"github.com/bwmarrin/discordgo"
)

type translateLanguage struct {
	Name         string
	SystemPrompt string
}

var translateLanguages = map[string]translateLanguage{
	"caveman": {
		Name: "Caveman",
		SystemPrompt: heredoc.Doc(`
			Translate the user's message to broken caveman english in roughly the same word length.
			Do not use em-dashes.
			ONLY reply with the translated text, nothing else.
		`),
	},
	"gen_alpha": {
		Name: "Gen Alpha",
		SystemPrompt: heredoc.Doc(`
			Translate the user's message to gen alpha slang (skibidi, rizz, no cap, bussin, slay, fr fr, sus, brainrot, sigma, gyat, mid, delulu, aura, cooked, fanum tax, NPC, dog water, touch grass, etc.) in roughly the same word length.
			Do not use em-dashes.
			ONLY reply with the translated text, nothing else.
		`),
	},
	"old_man": {
		Name: "Old Man",
		SystemPrompt: heredoc.Doc(`
			Translate the user's message to grumpy old man talk.
			Use phrases like "back in my day", "what? speak up!", "you kids these days", "when I was your age", "they don't make 'em like they used to", "turn that racket down!", "I tell ya", "the world's gone crazy", etc.
			Keep roughly the same word length.
			Do not use em-dashes.
			ONLY reply with the translated text, nothing else.
		`),
	},
	"80s": {
		Name: "80's",
		SystemPrompt: heredoc.Doc(`
			Translate the user's message to 1980s slang.
			Use phrases like "totally tubular", "gnarly", "radical", "rad", "gag me with a spoon", "like, totally", "bodacious", "righteous", "bogus", "grody", "bitchin'", "fresh", "take a chill pill", "no duh", etc.
			Keep roughly the same word length.
			Do not use em-dashes.
			ONLY reply with the translated text, nothing else.
		`),
	},
	"high_society": {
		Name: "High Society",
		SystemPrompt: heredoc.Doc(`
			Translate the user's message to fancy high society English.
			Use eloquent, sophisticated vocabulary, formal phrasing, and an air of aristocratic refinement.
			Think Victorian upper class, "indeed", "most assuredly", "I dare say", "one finds", "indubitably", etc.
			Keep roughly the same word length.
			Do not use em-dashes.
			ONLY reply with the translated text, nothing else.
		`),
	},
	"doakes": {
		Name: "Sergeant Doakes",
		SystemPrompt: heredoc.Doc(`
			Translate the user's message in the style of Sergeant Doakes from Dexter.
			He is intense, suspicious, confrontational, and always thinks something shady is going on.
			Use phrases like "surprise, motherfucker", "stop grinning like a fucking psycho", "I knew there was something wrong with you", "stay out of my way", "you think I don't see what you're doing?", "motherfucker", etc.
			Everything should drip with paranoid suspicion and barely-contained rage.
			Keep roughly the same word length.
			Do not use em-dashes.
			ONLY reply with the translated text, nothing else.
		`),
	},
}

// translateLanguageKeys returns a stable list of language keys for random selection
var translateLanguageKeys = func() []string {
	keys := make([]string, 0, len(translateLanguages))
	for k := range translateLanguages {
		keys = append(keys, k)
	}
	return keys
}()

// getTranslateLanguage returns the configured translate language, resolving "random" to a random pick.
func (m *FunModule) getTranslateLanguage() translateLanguage {
	key := m.config.GetTranslateLanguage()
	if key == "random" {
		key = translateLanguageKeys[rand.Intn(len(translateLanguageKeys))]
	}
	lang, ok := translateLanguages[key]
	if !ok {
		// Fall back to caveman if the config value is invalid
		return translateLanguages["caveman"]
	}
	return lang
}

// FunModule implements the CommandModule interface for fun commands
type FunModule struct {
	config *config.Config
}

// New creates a new fun module
func New(deps *types.Dependencies) *FunModule {
	return &FunModule{
		config: deps.Config,
	}
}

// Register adds fun-related commands to the command map
func (m *FunModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	var modPerms int64 = discordgo.PermissionBanMembers

	cmds["typing"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "typing",
			Description:              "Make the bot show as typing in a channel",
			DefaultMemberPermissions: &modPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "channel",
					Description: "The channel to type in",
					Required:    true,
					ChannelTypes: []discordgo.ChannelType{
						discordgo.ChannelTypeGuildText,
						discordgo.ChannelTypeGuildNews,
						discordgo.ChannelTypeGuildPublicThread,
						discordgo.ChannelTypeGuildPrivateThread,
						discordgo.ChannelTypeGuildNewsThread,
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "minutes",
					Description: "How many minutes to show as typing (1-10)",
					Required:    true,
					MinValue:    utils.Float64Ptr(1),
					MaxValue:    10,
				},
			},
		},
		HandlerFunc: m.handleTyping,
	}

	cmds["Translate"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "Translate",
			Type:                     discordgo.MessageApplicationCommand,
			DefaultMemberPermissions: &modPerms,
		},
		HandlerFunc: m.handleTranslate,
	}

	cmds["connect4"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "connect4",
			Description: "Challenge someone to a game of Connect 4",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "opponent",
					Description: "The player you want to challenge",
					Required:    true,
				},
			},
		},
		HandlerFunc: m.handleConnect4Challenge,
	}
}

// handleTranslate handles the "Translate" message context menu command.
func (m *FunModule) handleTranslate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	targetMsg := data.Resolved.Messages[data.TargetID]

	if targetMsg == nil || targetMsg.Content == "" {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ That message has no text content to translate.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	lang := m.getTranslateLanguage()

	// Defer so the user knows we're working on it
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	modelsClient := utils.NewModelsClient(m.config)
	translation := modelsClient.ModelsRequest(lang.SystemPrompt, targetMsg.Content, "deepseek/DeepSeek-V3-0324")

	if translation == "" {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: strPtr("❌ Failed to translate. The AI didn't return anything."),
		})
		return
	}

	// Format the raw translation into a quote block
	quoted := "> " + strings.ReplaceAll(strings.TrimSpace(translation), "\n", "\n> ")
	message := fmt.Sprintf("Hey, I translated this to %s for you:\n\n%s", lang.Name, quoted)

	// Reply to the original message in the channel
	_, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
		Content: message,
		Reference: &discordgo.MessageReference{
			MessageID: targetMsg.ID,
			ChannelID: i.ChannelID,
		},
	})
	if err != nil {
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: strPtr(fmt.Sprintf("❌ Translation succeeded but failed to send reply: %v", err)),
		})
		return
	}

	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: strPtr(fmt.Sprintf("✅ %s translation sent!", lang.Name)),
	})
}

// Service returns nil as this module has no services requiring initialization
func (m *FunModule) Service() types.ModuleService {
	return nil
}

// handleTyping makes the bot show as typing in a channel for a specified duration.
func (m *FunModule) handleTyping(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	var channelID string
	var minutes int64

	for _, opt := range options {
		switch opt.Name {
		case "channel":
			ch := opt.ChannelValue(s)
			if ch != nil {
				channelID = ch.ID
			}
		case "minutes":
			minutes = opt.IntValue()
		}
	}

	ch, err := s.Channel(channelID)
	if err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Could not access that channel.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	perms, err := s.UserChannelPermissions(s.State.User.ID, channelID)
	if err != nil || perms&discordgo.PermissionSendMessages == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("❌ I don't have permission to send messages in %s.", ch.Mention()),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("⌨️ Typing in %s for %d minute(s)...", ch.Mention(), minutes),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	// Discord typing indicator lasts ~10 seconds, so we re-trigger every 8s
	go func() {
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		end := time.Now().Add(time.Duration(minutes) * time.Minute)

		// Fire immediately, then on each tick
		_ = s.ChannelTyping(channelID)
		for t := range ticker.C {
			if t.After(end) {
				return
			}
			if err := s.ChannelTyping(channelID); err != nil {
				m.config.Logger.Warnf("typing loop stopped for channel %s: %v", channelID, err)
				return
			}
		}
	}()
}

func strPtr(s string) *string {
	return &s
}
