package events

import (
	"fmt"
	"time"

	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
)

// isVoiceSyncChannel returns true if the channel is a voice channel inside the configured sync category.
func isVoiceSyncChannel(ch *discordgo.Channel, cfg *config.Config) bool {
	if ch.Type != discordgo.ChannelTypeGuildVoice {
		return false
	}
	voiceSyncCategoryID := cfg.GetGamerPalsVoiceSyncCategoryID()
	return voiceSyncCategoryID != "" && ch.ParentID == voiceSyncCategoryID
}

// findEveryoneOverwrite returns the @everyone permission overwrite for the channel, or nil.
func findEveryoneOverwrite(ch *discordgo.Channel) *discordgo.PermissionOverwrite {
	for _, po := range ch.PermissionOverwrites {
		if po.ID == ch.GuildID && po.Type == discordgo.PermissionOverwriteTypeRole {
			return po
		}
	}
	return nil
}

// HandleVoicePermissionSyncUpdate handles syncing View Channel permission with Connect permission
// for voice channels in the configured category.
func HandleVoicePermissionSyncUpdate(s *discordgo.Session, c *discordgo.ChannelUpdate, cfg *config.Config) {
	if !isVoiceSyncChannel(c.Channel, cfg) {
		return
	}

	var beforeOverwrite *discordgo.PermissionOverwrite
	if c.BeforeUpdate != nil {
		beforeOverwrite = findEveryoneOverwrite(c.BeforeUpdate)
	}
	afterOverwrite := findEveryoneOverwrite(c.Channel)

	// Check if Connect permission changed
	beforeConnectDenied := beforeOverwrite != nil && (beforeOverwrite.Deny&discordgo.PermissionVoiceConnect) != 0
	afterConnectDenied := afterOverwrite != nil && (afterOverwrite.Deny&discordgo.PermissionVoiceConnect) != 0

	if beforeConnectDenied == afterConnectDenied {
		return
	}

	afterViewDenied := afterOverwrite != nil && (afterOverwrite.Deny&discordgo.PermissionViewChannel) != 0

	if afterConnectDenied && !afterViewDenied {
		cfg.Logger.Infof("Voice channel %s (%s): Connect denied, syncing View Channel to denied", c.Name, c.ID)
		syncViewPermission(s, c.Channel, cfg, true)
	} else if !afterConnectDenied && afterViewDenied {
		cfg.Logger.Infof("Voice channel %s (%s): Connect allowed, syncing View Channel to allowed", c.Name, c.ID)
		syncViewPermission(s, c.Channel, cfg, false)
	}
}

// HandleVoicePermissionSyncCreate handles syncing View Channel permission for newly created
// voice channels that already have Connect denied.
func HandleVoicePermissionSyncCreate(s *discordgo.Session, c *discordgo.ChannelCreate, cfg *config.Config) {
	if !isVoiceSyncChannel(c.Channel, cfg) {
		return
	}

	overwrite := findEveryoneOverwrite(c.Channel)
	connectDenied := overwrite != nil && (overwrite.Deny&discordgo.PermissionVoiceConnect) != 0
	viewDenied := overwrite != nil && (overwrite.Deny&discordgo.PermissionViewChannel) != 0

	if connectDenied && !viewDenied {
		cfg.Logger.Infof("Voice channel %s (%s): created with Connect denied, syncing View Channel to denied", c.Name, c.ID)
		syncViewPermission(s, c.Channel, cfg, true)
	}
}

// syncViewPermission updates the @everyone View Channel permission
func syncViewPermission(s *discordgo.Session, ch *discordgo.Channel, cfg *config.Config, denyView bool) {
	currentOverwrite := findEveryoneOverwrite(ch)

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

	err := s.ChannelPermissionSet(ch.ID, ch.GuildID, discordgo.PermissionOverwriteTypeRole, allow, deny)
	if err != nil {
		cfg.Logger.Errorf("Failed to sync View permission for channel %s: %v", ch.ID, err)
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
				Value:  fmt.Sprintf("<#%s>", ch.ID),
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
