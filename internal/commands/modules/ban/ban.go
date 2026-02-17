package ban

import (
	"fmt"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
)

const (
	contextMenuReason = "banned from context menu"
	banDMMessage      = "You have been banned from GamerPals. See https://gamerpals.xyz/docs/info/moderation-policies/#appealing-punishments"
)

type banOpts struct {
	CreateBan    func(s *discordgo.Session, guildID, userID, reason string, days int) error
	Respond      func(s *discordgo.Session, i *discordgo.Interaction, resp *discordgo.InteractionResponse) error
	EditResponse func(s *discordgo.Session, i *discordgo.Interaction, edit *discordgo.WebhookEdit) error
	SendDM       func(s *discordgo.Session, userID, message string) error
	LogToChannel func(cfg *config.Config, s *discordgo.Session, channelID string, embed *discordgo.MessageEmbed) error
	LogToBestPal func(cfg *config.Config, s *discordgo.Session, msg string) error
}

func defaultBanOpts() banOpts {
	return banOpts{
		CreateBan:    createBan,
		Respond:      respond,
		EditResponse: editResponse,
		SendDM:       sendDM,
		LogToChannel: logToChannel,
		LogToBestPal: logToBestPal,
	}
}

func createBan(s *discordgo.Session, guildID, userID, reason string, days int) error {
	return s.GuildBanCreateWithReason(guildID, userID, reason, days)
}

func respond(s *discordgo.Session, i *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
	return s.InteractionRespond(i, resp)
}

func editResponse(s *discordgo.Session, i *discordgo.Interaction, edit *discordgo.WebhookEdit) error {
	_, err := s.InteractionResponseEdit(i, edit)
	return err
}

func sendDM(s *discordgo.Session, userID, message string) error {
	ch, err := s.UserChannelCreate(userID)
	if err != nil {
		return err
	}
	_, err = s.ChannelMessageSend(ch.ID, message)
	return err
}

func logToChannel(_ *config.Config, s *discordgo.Session, channelID string, embed *discordgo.MessageEmbed) error {
	_, err := s.ChannelMessageSendEmbed(channelID, embed)
	return err
}

func logToBestPal(cfg *config.Config, s *discordgo.Session, msg string) error {
	id := cfg.GetGamerpalsLogChannelID()
	if id == "" {
		return nil
	}
	_, err := s.ChannelMessageSendEmbed(id, &discordgo.MessageEmbed{
		Title:       "Best Pal Message",
		Description: msg,
		Color:       0x2176ae,
		Timestamp:   time.Now().Format(time.RFC3339),
	})
	return err
}

type BanModule struct {
	config *config.Config
	opts   banOpts
}

func New(deps *types.Dependencies) types.CommandModule {
	return &BanModule{
		config: deps.Config,
		opts:   defaultBanOpts(),
	}
}

func (m *BanModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	var banPerms int64 = discordgo.PermissionBanMembers
	guildOnly := &[]discordgo.InteractionContextType{
		discordgo.InteractionContextGuild,
	}

	cmds["ban"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "ban",
			Description:              "Ban a user from the server",
			DefaultMemberPermissions: &banPerms,
			Contexts:                 guildOnly,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "The user to ban",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "days",
					Description: "Number of days of messages to purge (default: 0)",
					Required:    false,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "Don't delete any", Value: 0},
						{Name: "1 day", Value: 1},
						{Name: "2 days", Value: 2},
						{Name: "3 days", Value: 3},
						{Name: "4 days", Value: 4},
						{Name: "5 days", Value: 5},
						{Name: "6 days", Value: 6},
						{Name: "7 days", Value: 7},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "reason",
					Description: "Reason for the ban (shown in audit log)",
					Required:    false,
				},
			},
		},
		HandlerFunc: m.handleBanSlash,
	}

	cmds["Ban User"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "Ban User",
			Type:                     discordgo.UserApplicationCommand,
			DefaultMemberPermissions: &banPerms,
			Contexts:                 guildOnly,
		},
		HandlerFunc: m.handleBanContext,
	}

	cmds["Ban + Purge Messages"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "Ban + Purge Messages",
			Type:                     discordgo.UserApplicationCommand,
			DefaultMemberPermissions: &banPerms,
			Contexts:                 guildOnly,
		},
		HandlerFunc: m.handleBanContext,
	}
}

func (m *BanModule) handleBanSlash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()

	// Defer response immediately to avoid 3s timeout
	m.deferEphemeral(s, i)

	if i.Member == nil || i.Member.User == nil {
		m.editEphemeral(s, i, "❌ This command can only be used in a server.")
		return
	}

	// Parse options by name, not position
	var targetID, reason string
	days := 0
	for _, opt := range data.Options {
		switch opt.Name {
		case "user":
			targetID = opt.Value.(string)
		case "days":
			days = int(opt.IntValue())
		case "reason":
			reason = opt.StringValue()
		}
	}

	if targetID == "" {
		m.editEphemeral(s, i, "❌ No user specified.")
		return
	}

	if data.Resolved == nil || data.Resolved.Users[targetID] == nil {
		m.editEphemeral(s, i, "❌ Could not resolve the specified user.")
		return
	}
	targetUser := data.Resolved.Users[targetID]

	if err := m.validateTarget(s, i, targetUser.ID); err != nil {
		m.editEphemeral(s, i, err.Error())
		return
	}

	if days < 0 || days > 7 {
		m.editEphemeral(s, i, "❌ Days must be between 0 and 7.")
		return
	}

	m.executeBan(s, i, targetUser, reason, days, "slash command")
}

func (m *BanModule) handleBanContext(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()

	// Defer response immediately to avoid 3s timeout
	m.deferEphemeral(s, i)

	if i.Member == nil || i.Member.User == nil {
		m.editEphemeral(s, i, "❌ This command can only be used in a server.")
		return
	}

	targetID := data.TargetID
	if data.Resolved == nil || data.Resolved.Users[targetID] == nil {
		m.editEphemeral(s, i, "❌ Could not resolve the target user.")
		return
	}
	targetUser := data.Resolved.Users[targetID]

	if err := m.validateTarget(s, i, targetUser.ID); err != nil {
		m.editEphemeral(s, i, err.Error())
		return
	}

	days := 0
	if data.Name == "Ban + Purge Messages" {
		days = 7
	}

	m.executeBan(s, i, targetUser, contextMenuReason, days, "context menu")
}

func (m *BanModule) validateTarget(s *discordgo.Session, i *discordgo.InteractionCreate, targetID string) error {
	if targetID == i.Member.User.ID {
		return fmt.Errorf("❌ You cannot ban yourself.")
	}
	if targetID == s.State.User.ID {
		return fmt.Errorf("❌ I cannot ban myself.")
	}
	return nil
}

func (m *BanModule) executeBan(s *discordgo.Session, i *discordgo.InteractionCreate, targetUser *discordgo.User, reason string, days int, source string) {
	guildID := i.GuildID

	// DM the user before banning (can't DM after they leave the guild)
	if err := m.opts.SendDM(s, targetUser.ID, banDMMessage); err != nil {
		_ = m.opts.LogToBestPal(m.config, s, fmt.Sprintf("⚠️ Could not DM <@%s> (%s) before banning — they may have DMs disabled.", targetUser.ID, targetUser.Username))
	}

	// Execute the ban
	if err := m.opts.CreateBan(s, guildID, targetUser.ID, reason, days); err != nil {
		m.editEphemeral(s, i, fmt.Sprintf("❌ Failed to ban user: %v", err))
		return
	}

	// Log to mod action log channel
	m.logModAction(s, i, targetUser, reason, days, source)

	displayReason := reason
	if displayReason == "" {
		displayReason = "No reason provided"
	}
	m.editEphemeral(s, i, fmt.Sprintf("✅ Banned **%s** (%s). Reason: %s. Messages purged: %d day(s).", targetUser.Username, targetUser.ID, displayReason, days))
}

func (m *BanModule) logModAction(s *discordgo.Session, i *discordgo.InteractionCreate, targetUser *discordgo.User, reason string, days int, source string) {
	channelID := m.config.GetGamerPalsModActionLogChannelID()
	if channelID == "" {
		return
	}

	displayReason := reason
	if displayReason == "" {
		displayReason = "No reason provided"
	}

	embed := &discordgo.MessageEmbed{
		Title: "🔨 User Banned",
		Color: 0xd33f49,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Banned User", Value: fmt.Sprintf("<@%s> (%s)", targetUser.ID, targetUser.ID), Inline: true},
			{Name: "Banned By", Value: fmt.Sprintf("<@%s> (%s)", i.Member.User.ID, i.Member.User.ID), Inline: true},
			{Name: "Reason", Value: displayReason, Inline: false},
			{Name: "Messages Purged", Value: fmt.Sprintf("%d day(s)", days), Inline: true},
			{Name: "Source", Value: source, Inline: true},
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	_ = m.opts.LogToChannel(m.config, s, channelID, embed)
}

func (m *BanModule) deferEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = m.opts.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
}

func (m *BanModule) editEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_ = m.opts.EditResponse(s, i.Interaction, &discordgo.WebhookEdit{
		Content: &content,
	})
}

func (m *BanModule) Service() types.ModuleService {
	return nil
}
