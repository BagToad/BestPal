package scamguard

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// HandleComponent handles moderation controls attached to scam detections.
// Permission is enforced at click time; Discord component custom IDs are not
// an authorization mechanism.
func (m *Module) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.MessageComponentData().CustomID
	if !strings.HasPrefix(customID, banScamButtonPrefix) {
		return
	}

	respond := func(content string) {
		_ = m.respond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: content,
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
	}

	if !m.hasBanPermissions(s, i) {
		respond("❌ You need the Ban Members permission to use this button.")
		return
	}

	targetID := strings.TrimPrefix(customID, banScamButtonPrefix)
	if targetID == "" || i.GuildID == "" {
		respond("❌ This ban action is invalid or has expired.")
		return
	}

	if err := m.createBan(s, i.GuildID, targetID, "Scam image detected", 0); err != nil {
		respond(fmt.Sprintf("❌ Failed to ban user: %v", err))
		return
	}
	respond(fmt.Sprintf("✅ Banned <@%s>.", targetID))
}
