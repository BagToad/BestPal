package config

import (
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// ConfigModule renders the Discord-native config panel. It is generic: every
// row comes from the settings registry collected at startup, so a module that
// declares a new setting gets a panel row, persistence, and validation for
// free. Access is gated by the Ban Members permission (or super admin).
type ConfigModule struct {
	config *config.Config
}

// New creates a new config module.
func New(deps *types.Dependencies) *ConfigModule {
	return &ConfigModule{config: deps.Config}
}

// Register adds the /config command. DefaultMemberPermissions hides it from
// members without Ban Members; the handler re-checks server-side.
func (m *ConfigModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	m.config = deps.Config

	banPerms := int64(discordgo.PermissionBanMembers)
	cmds["config"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "config",
			Description:              "Configure the bot for this server (Ban Members required)",
			DefaultMemberPermissions: &banPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		},
		HandlerFunc: m.handleConfig,
	}
}

// Service returns nil; the config module has no scheduled service.
func (m *ConfigModule) Service() types.ModuleService { return nil }

// handleConfig is the /config entrypoint: it gates access and renders the home
// panel as an ephemeral message.
func (m *ConfigModule) handleConfig(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !m.canManage(i) {
		respondEphemeral(s, i, "❌ You need the Ban Members permission to configure the bot.")
		return
	}
	if i.GuildID == "" {
		respondEphemeral(s, i, "❌ Run this in a server, not a DM.")
		return
	}
	resp := m.renderHome(i.GuildID)
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: resp,
	})
}

// HandleComponent routes config: component interactions (buttons, selects).
func (m *ConfigModule) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !m.canManage(i) {
		respondEphemeral(s, i, "❌ You need the Ban Members permission to configure the bot.")
		return
	}
	cid := i.MessageComponentData().CustomID
	rest := strings.TrimPrefix(cid, customIDPrefix)
	switch {
	case rest == actHome:
		m.updateToHome(s, i)
	case strings.HasPrefix(rest, actCategory+":"):
		m.updateToCategory(s, i, strings.TrimPrefix(rest, actCategory+":"))
	case strings.HasPrefix(rest, actPick+":"):
		m.handlePick(s, i, strings.TrimPrefix(rest, actPick+":"))
	case strings.HasPrefix(rest, actToggle+":"):
		m.handleToggle(s, i, strings.TrimPrefix(rest, actToggle+":"))
	case strings.HasPrefix(rest, actEdit+":"):
		m.handleOpenEditModal(s, i, strings.TrimPrefix(rest, actEdit+":"))
	default:
		m.config.Logger.Warnf("config panel: unhandled component customID %q", cid)
		respondEphemeral(s, i, "❌ Unknown action. Run /config again.")
	}
}

// HandleModalSubmit routes config: modal submissions.
func (m *ConfigModule) HandleModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !m.canManage(i) {
		respondEphemeral(s, i, "❌ You need the Ban Members permission to configure the bot.")
		return
	}
	cid := i.ModalSubmitData().CustomID
	rest := strings.TrimPrefix(cid, customIDPrefix)
	if strings.HasPrefix(rest, actModal+":") {
		m.handleEditModalSubmit(s, i, strings.TrimPrefix(rest, actModal+":"))
		return
	}
	m.config.Logger.Warnf("config panel: unhandled modal customID %q", cid)
	respondEphemeral(s, i, "❌ Unknown form. Run /config again.")
}

// canManage reports whether the interacting user may use the config panel:
// super admins always, otherwise members with Ban Members or Administrator.
func (m *ConfigModule) canManage(i *discordgo.InteractionCreate) bool {
	userID := interactionUserID(i)
	if userID != "" && utils.IsSuperAdmin(userID, m.config) {
		return true
	}
	if i.Member == nil {
		return false
	}
	const modBits = discordgo.PermissionBanMembers | discordgo.PermissionAdministrator
	return i.Member.Permissions&modBits != 0
}

// interactionUserID returns the acting user's ID for both guild (Member) and
// DM (User) interactions.
func interactionUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

// respondEphemeral sends a simple ephemeral text response.
func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}
