package pomo

import (
	"fmt"
	"io"
	"net/http"

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
	cmds["pomo-music"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "pomo-music",
			Description: "Set focus music for your pomodoro session",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionAttachment,
					Name:        "audio",
					Description: "Audio file to play during focus sessions (mp3, wav, ogg, etc.)",
					Required:    true,
				},
			},
		},
		HandlerFunc: m.handlePomoMusic,
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

// handlePomoMusic handles the /pomo-music slash command
func (m *PomoModule) handlePomoMusic(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Validate: must be in a voice channel text chat
	channel, err := s.Channel(i.ChannelID)
	if err != nil || channel.Type != discordgo.ChannelTypeGuildVoice {
		respondEphemeral(s, i, "❌ This command must be run in a voice channel's text chat.")
		return
	}
	voiceChannelID := channel.ID

	// Must have an active pomo session
	ps := GetSession(voiceChannelID)
	if ps == nil {
		respondEphemeral(s, i, "❌ No active pomodoro session. Run `/pomo` first.")
		return
	}

	// Get the attachment
	opts := i.ApplicationCommandData().Options
	if len(opts) == 0 {
		respondEphemeral(s, i, "❌ Please provide an audio file.")
		return
	}
	attachmentID := opts[0].Value.(string)
	resolved := i.ApplicationCommandData().Resolved
	if resolved == nil || resolved.Attachments == nil {
		respondEphemeral(s, i, "❌ Could not resolve attachment.")
		return
	}
	attachment, ok := resolved.Attachments[attachmentID]
	if !ok {
		respondEphemeral(s, i, "❌ Could not find attachment.")
		return
	}

	// Size limit: 25 MB
	const maxSize = 25 * 1024 * 1024
	if attachment.Size > maxSize {
		respondEphemeral(s, i, "❌ File too large. Maximum size is 25 MB.")
		return
	}

	// Defer response since conversion may take a few seconds
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	// Download the attachment
	resp, err := http.Get(attachment.URL)
	if err != nil {
		m.editResponse(s, i, "❌ Failed to download file.")
		return
	}
	defer resp.Body.Close()

	rawAudio, err := io.ReadAll(resp.Body)
	if err != nil {
		m.editResponse(s, i, "❌ Failed to read file.")
		return
	}

	// Convert to opus frames via ffmpeg
	opusFrames, err := convertToOpusFrames(rawAudio)
	if err != nil {
		m.config.Logger.Errorf("Pomo: ffmpeg conversion failed: %v", err)
		m.editResponse(s, i, fmt.Sprintf("❌ Failed to convert audio. Make sure it's a valid audio file.\n```%v```", err))
		return
	}

	// Set the track on the session
	ps.SetMusicTrack(opusFrames)

	m.editResponse(s, i, fmt.Sprintf("🎵 Music set! **%s** will play during focus sessions.", attachment.Filename))
}

// editResponse edits a deferred interaction response
func (m *PomoModule) editResponse(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &msg,
	})
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

// HandleVoiceStateUpdate checks whether a voice channel with an active pomo
// session has been emptied (all humans left). If so, it resets and cleans up.
func (m *PomoModule) HandleVoiceStateUpdate(s *discordgo.Session, vs *discordgo.VoiceStateUpdate) {
	// We care about users leaving a channel (ChannelID empty or changed).
	// Check the *previous* channel — if it had a pomo session and is now
	// empty of humans, tear it down.
	if vs.BeforeUpdate == nil {
		return
	}
	prevChannel := vs.BeforeUpdate.ChannelID
	if prevChannel == "" {
		return
	}
	// Only act if the user actually left this channel
	if vs.ChannelID == prevChannel {
		return
	}

	ps := GetSession(prevChannel)
	if ps == nil {
		return
	}

	// Count humans still in the channel (exclude bots)
	guild, err := s.State.Guild(vs.GuildID)
	if err != nil {
		return
	}
	botID := s.State.User.ID
	humans := 0
	for _, v := range guild.VoiceStates {
		if v.ChannelID == prevChannel && v.UserID != botID {
			humans++
		}
	}

	if humans > 0 {
		return
	}

	m.config.Logger.Infof("Pomo: voice channel %s is empty, resetting session", prevChannel)
	go func() {
		ps.Reset()
		RemoveSession(prevChannel)
	}()
}
