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

	if !m.hasBanPermissions(i) {
		respond("❌ You need the Ban Members permission to use this button.")
		return
	}

	targetID := strings.TrimPrefix(customID, banScamButtonPrefix)
	if targetID == "" || i.GuildID == "" {
		respond("❌ This ban action is invalid or has expired.")
		return
	}

	// DM the user before banning so the message delivers before they lose guild access.
	const scamBanDMMessage = "You have been banned from GamerPals. Reason: Scam image detected.\n\nSee https://gamerpals.xyz/docs/info/moderation-policies/#appealing-punishments"
	if err := m.sendDM(s, targetID, scamBanDMMessage); err != nil {
		m.config.Logger.Warnf("scamguard: could not DM %s before banning (DMs may be disabled): %v", targetID, err)
	}

	if err := m.createBan(s, i.GuildID, targetID, "Scam image detected", 0); err != nil {
		respond(fmt.Sprintf("❌ Failed to ban user: %v", err))
		return
	}

	// Edit the original log embed to reflect the ban and remove the button.
	modID := i.Member.User.ID
	var updatedEmbed *discordgo.MessageEmbed
	if i.Message != nil && len(i.Message.Embeds) > 0 {
		embedCopy := *i.Message.Embeds[0]
		embedCopy.Fields = append(embedCopy.Fields, &discordgo.MessageEmbedField{
			Name:   "Banned by",
			Value:  fmt.Sprintf("<@%s>", modID),
			Inline: true,
		})
		embedCopy.Color = 0x36393f // muted/resolved
		updatedEmbed = &embedCopy
	}

	if updatedEmbed == nil {
		respond(fmt.Sprintf("✅ Banned <@%s>.", targetID))
		return
	}

	_ = m.respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{updatedEmbed},
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "Ban",
						Style:    discordgo.DangerButton,
						CustomID: customID,
						Disabled: true,
					},
				}},
			},
		},
	})
}
