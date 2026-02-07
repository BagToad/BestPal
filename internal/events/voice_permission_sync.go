package events

import (
	"fmt"
	"time"

	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
)

// OnVoicePermissionSync handles syncing View Channel permission with Connect permission
// for voice channels in the configured category.
func OnVoicePermissionSync(s *discordgo.Session, c *discordgo.ChannelUpdate, cfg *config.Config) {
	// Only process voice channels
	if c.Type != discordgo.ChannelTypeGuildVoice {
		return
	}

	// Check if voice sync category is configured
	voiceSyncCategoryID := cfg.GetGamerPalsVoiceSyncCategoryID()
	if voiceSyncCategoryID == "" {
		return
	}

	// Only process channels in the configured category
	if c.ParentID != voiceSyncCategoryID {
		return
	}

	// Get the @everyone role overwrite (ID equals GuildID)
	var beforeOverwrite, afterOverwrite *discordgo.PermissionOverwrite

	if c.BeforeUpdate != nil {
		for _, po := range c.BeforeUpdate.PermissionOverwrites {
			// the @everyone role's ID is always equal to the guild (server) ID.
			if po.ID == c.GuildID && po.Type == discordgo.PermissionOverwriteTypeRole {
				beforeOverwrite = po
				break
			}
		}
	}

	for _, po := range c.PermissionOverwrites {
		if po.ID == c.GuildID && po.Type == discordgo.PermissionOverwriteTypeRole {
			afterOverwrite = po
			break
		}
	}

	// Check if Connect permission changed
	beforeConnectDenied := beforeOverwrite != nil && (beforeOverwrite.Deny&discordgo.PermissionVoiceConnect) != 0
	afterConnectDenied := afterOverwrite != nil && (afterOverwrite.Deny&discordgo.PermissionVoiceConnect) != 0

	// No change in Connect deny state
	if beforeConnectDenied == afterConnectDenied {
		return
	}

	// Determine current View state to avoid unnecessary updates
	afterViewDenied := afterOverwrite != nil && (afterOverwrite.Deny&discordgo.PermissionViewChannel) != 0

	// If Connect is now denied and View is not already denied, deny View
	if afterConnectDenied && !afterViewDenied {
		cfg.Logger.Infof("Voice channel %s (%s): Connect denied, syncing View Channel to denied", c.Name, c.ID)
		syncViewPermission(s, c, cfg, true)
		return
	}

	// If Connect is now allowed and View is currently denied, allow View
	if !afterConnectDenied && afterViewDenied {
		cfg.Logger.Infof("Voice channel %s (%s): Connect allowed, syncing View Channel to allowed", c.Name, c.ID)
		syncViewPermission(s, c, cfg, false)
		return
	}
}

// syncViewPermission updates the @everyone View Channel permission
func syncViewPermission(s *discordgo.Session, c *discordgo.ChannelUpdate, cfg *config.Config, denyView bool) {
	// Find current @everyone overwrite
	var currentOverwrite *discordgo.PermissionOverwrite
	for _, po := range c.PermissionOverwrites {
		if po.ID == c.GuildID && po.Type == discordgo.PermissionOverwriteTypeRole {
			currentOverwrite = po
			break
		}
	}

	var allow, deny int64
	if currentOverwrite != nil {
		allow = currentOverwrite.Allow
		deny = currentOverwrite.Deny
	}

	if denyView {
		// Add View Channel to deny, remove from allow
		deny |= discordgo.PermissionViewChannel
		allow &^= discordgo.PermissionViewChannel
	} else {
		// Remove View Channel from deny
		deny &^= discordgo.PermissionViewChannel
	}

	err := s.ChannelPermissionSet(c.ID, c.GuildID, discordgo.PermissionOverwriteTypeRole, allow, deny)
	if err != nil {
		cfg.Logger.Errorf("Failed to sync View permission for channel %s: %v", c.ID, err)
		return
	}

	// Log to Discord channel
	logChannelID := cfg.GetGamerpalsLogChannelID()
	if logChannelID == "" {
		return
	}

	action := "denied"
	color := 0xFF6B6B // Red for deny
	if !denyView {
		action = "allowed"
		color = 0x4ECDC4 // Teal for allow
	}

	embed := &discordgo.MessageEmbed{
		Title:       "🔊 Voice Permission Sync",
		Description: "Automatically synced View Channel permission for voice channel.",
		Color:       color,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Channel",
				Value:  fmt.Sprintf("<#%s>", c.ID),
				Inline: true,
			},
			{
				Name:   "Action",
				Value:  fmt.Sprintf("View Channel %s for @everyone", action),
				Inline: true,
			},
		},
		Timestamp: time.Now().Format(time.RFC3339),
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Voice Permission Sync",
		},
	}

	_, err = s.ChannelMessageSendEmbed(logChannelID, embed)
	if err != nil {
		cfg.Logger.Errorf("Failed to send voice sync log message: %v", err)
	}
}
