// Package tts implements the /tts voice command: it makes the bot join the
// caller's voice channel and speak supplied text using a fully offline,
// pure-Go text-to-speech engine (see internal/speech). No API keys, network
// calls, or cgo are involved.
//
// The audio path does not use discordgo's voice support, which only implements
// the deprecated xsalsa20_poly1305 transport mode that Discord no longer
// accepts. Instead the join is driven through discordgo's main gateway
// (ChannelVoiceJoinManual) and the media path is handled by the internal/voice
// package, which speaks the current voice gateway and AEAD-encrypted RTP
// protocol. See internal/voice for details.
package tts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
)

// maxTTSChars caps how much text we will synthesize per command.
const maxTTSChars = 500

// Module implements types.CommandModule for the /tts command. It exposes
// OnVoiceServerUpdate / OnVoiceStateUpdate handlers (wired in bot.go) that feed
// the voice handshake.
type Module struct {
	config  *config.Config
	service *Service
}

// New creates a new tts module.
func New(deps *types.Dependencies) *Module {
	return &Module{
		config:  deps.Config,
		service: NewService(deps.Config),
	}
}

// Register adds the /tts command to the command map.
func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	cmds["tts"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "tts",
			Description: "Join your voice channel and speak text aloud",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "text",
					Description: "What the bot should say",
					Required:    true,
					MaxLength:   maxTTSChars,
				},
			},
		},
		HandlerFunc: m.handleTTS,
	}
}

// Service returns the TTS service so the bot can hydrate it and wire handlers.
func (m *Module) Service() types.ModuleService { return m.service }

// OnVoiceServerUpdate forwards voice server updates to the service.
func (m *Module) OnVoiceServerUpdate(s *discordgo.Session, e *discordgo.VoiceServerUpdate) {
	m.service.OnVoiceServerUpdate(s, e)
}

// OnVoiceStateUpdate forwards voice state updates to the service.
func (m *Module) OnVoiceStateUpdate(s *discordgo.Session, e *discordgo.VoiceStateUpdate) {
	m.service.OnVoiceStateUpdate(s, e)
}

func (m *Module) handleTTS(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	text := strings.TrimSpace(optionString(i, "text"))
	if text == "" {
		m.edit(s, i, "❌ Please provide some text to speak.")
		return
	}
	if utf8.RuneCountInString(text) > maxTTSChars {
		m.edit(s, i, fmt.Sprintf("❌ Text is too long (max %d characters).", maxTTSChars))
		return
	}

	guildID := i.GuildID
	if guildID == "" || i.Member == nil || i.Member.User == nil {
		m.edit(s, i, "❌ This command can only be used in a server.")
		return
	}

	// Find the caller's current voice channel from cached state.
	vs, err := s.State.VoiceState(guildID, i.Member.User.ID)
	if err != nil || vs == nil || vs.ChannelID == "" {
		m.edit(s, i, "❌ You need to be in a voice channel first.")
		return
	}

	if err := m.checkVoicePermissions(s, guildID, vs.ChannelID); err != nil {
		m.edit(s, i, "❌ "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := m.service.Speak(ctx, guildID, vs.ChannelID, text); err != nil {
		if errors.Is(err, ErrBusy) {
			m.edit(s, i, "⏳ "+err.Error())
			return
		}
		m.config.Logger.Errorf("tts: speak failed: %v", err)
		m.edit(s, i, "❌ Failed to speak: "+err.Error())
		return
	}

	m.edit(s, i, "🗣️ Done speaking.")
}

// checkVoicePermissions verifies the bot can connect to and speak in the channel.
func (m *Module) checkVoicePermissions(s *discordgo.Session, guildID, channelID string) error {
	botID := ""
	if s.State != nil && s.State.User != nil {
		botID = s.State.User.ID
	}
	perms, err := s.State.UserChannelPermissions(botID, channelID)
	if err != nil {
		// If permissions can't be computed from state, let the join attempt
		// surface any real failure rather than blocking here.
		return nil
	}
	if perms&discordgo.PermissionVoiceConnect == 0 {
		return errors.New("I don't have permission to connect to your voice channel.")
	}
	if perms&discordgo.PermissionVoiceSpeak == 0 {
		return errors.New("I don't have permission to speak in your voice channel.")
	}
	return nil
}

func (m *Module) edit(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content})
}

// optionString returns the named string option, or "" if absent.
func optionString(i *discordgo.InteractionCreate, name string) string {
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == name {
			return opt.StringValue()
		}
	}
	return ""
}
