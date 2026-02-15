package pomo

import (
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	internalVoice "gamerpal/internal/voice"

	"github.com/bwmarrin/discordgo"
)

// PomoModule implements the CommandModule interface for the pomodoro command
type PomoModule struct {
	config   *config.Config
	voiceMgr *internalVoice.Manager
}

// New creates a new pomo module
func New(deps *types.Dependencies, voiceMgr *internalVoice.Manager) *PomoModule {
	return &PomoModule{config: deps.Config, voiceMgr: voiceMgr}
}

// Register adds the pomo command to the command map
func (m *PomoModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	cmds["pomo"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "pomo",
			Description: "Start a pomodoro timer in your voice channel",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		},
		HandlerFunc: m.handlePomo,
	}
}

// handlePomo handles the /pomo slash command
func (m *PomoModule) handlePomo(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Validate: command must be run in a voice channel's text chat
	channel, err := s.Channel(i.ChannelID)
	if err != nil || channel.Type != discordgo.ChannelTypeGuildVoice {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ This command must be run in a voice channel's text chat.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Validate: user must be in the voice channel
	voiceChannelID := channel.ID
	userID := ""
	if i.Member != nil {
		userID = i.Member.User.ID
	}

	if !isUserInVC(s, i.GuildID, userID, voiceChannelID) {
		respondEphemeral(s, i, "❌ You must be in this voice channel to start a pomodoro timer.")
		return
	}

	// Post the pomo control panel
	embed := panelEmbed(PhaseIdle, 0, 0, MaxPomos)
	buttons := panelButtons(PhaseIdle)

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: buttons,
		},
	})

	// Retrieve the posted message to get its ID for future edits
	msg, err := s.InteractionResponse(i.Interaction)
	if err != nil {
		m.config.Logger.Errorf("Pomo: failed to get interaction response message: %v", err)
		return
	}

	// Create or update the session for this voice channel
	GetOrCreateSession(s, m.config, m.voiceMgr, i.GuildID, voiceChannelID, i.ChannelID, msg.ID)
}

// HandleComponent handles component interactions for pomo buttons
func (m *PomoModule) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	cid := i.MessageComponentData().CustomID

	// Resolve the voice channel ID from the channel where the button was clicked
	channel, err := s.Channel(i.ChannelID)
	if err != nil || channel.Type != discordgo.ChannelTypeGuildVoice {
		respondEphemeral(s, i, "❌ This button only works in a voice channel's text chat.")
		return
	}
	voiceChannelID := channel.ID

	// Verify the user is in this voice channel
	userID := ""
	if i.Member != nil {
		userID = i.Member.User.ID
	}
	if !isUserInVC(s, i.GuildID, userID, voiceChannelID) {
		respondEphemeral(s, i, "❌ You must be in this voice channel to control the timer.")
		return
	}

	// Look up the session
	ps := GetSession(voiceChannelID)
	if ps == nil {
		respondEphemeral(s, i, "❌ No active pomodoro session. Run `/pomo` to create one.")
		return
	}

	switch cid {
	case buttonStart:
		// Acknowledge immediately, then start the session
		deferUpdate(s, i)
		ps.Start()

	case buttonStop:
		deferUpdate(s, i)
		ps.Stop()

	case buttonReset:
		deferUpdate(s, i)
		ps.Reset()

	default:
		respondEphemeral(s, i, "❌ Unknown action.")
	}
}

// isUserInVC checks if a user is in the specified voice channel
func isUserInVC(s *discordgo.Session, guildID, userID, voiceChannelID string) bool {
	if guildID == "" || userID == "" {
		return false
	}
	guild, err := s.State.Guild(guildID)
	if err != nil {
		return false
	}
	for _, vs := range guild.VoiceStates {
		if vs.UserID == userID && vs.ChannelID == voiceChannelID {
			return true
		}
	}
	return false
}

// respondEphemeral sends an ephemeral response to a component interaction
func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// deferUpdate acknowledges a component interaction without changing the message
// (the session will update the panel via message edit)
func deferUpdate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	})
}

// Service returns nil as this module has no scheduled services
func (m *PomoModule) Service() types.ModuleService {
	return nil
}
