package commands

import (
	"fmt"
	"strings"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// handleListScheduledSays lists up to the next 20 scheduled messages
func (h *SlashCommandHandler) handleListScheduledSays(s *discordgo.Session, i *discordgo.InteractionCreate) {
	list := h.ScheduleSayService.List(20)
	if len(list) == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "No scheduled messages.", Flags: discordgo.MessageFlagsEphemeral}})
		return
	}

	// Build fields; ensure we don't exceed embed field limits (25) - we cap at 20 anyway.
	fields := make([]*discordgo.MessageEmbedField, 0, len(list))
	for _, m := range list {
		preview := m.Content
		if len(preview) > 10 { preview = preview[:10] }
		name := fmt.Sprintf("ID %d", m.ID)
		valueBuilder := strings.Builder{}
		valueBuilder.WriteString(fmt.Sprintf("Channel: <#%s>\n", m.ChannelID))
		valueBuilder.WriteString(fmt.Sprintf("Fire: <t:%d:F> (\n<t:%d:R>)\n", m.FireAt.Unix(), m.FireAt.Unix()))
		valueBuilder.WriteString(fmt.Sprintf("Suppress Footer: %v\n", m.SuppressModMessage))
		valueBuilder.WriteString(fmt.Sprintf("Preview: %.10q", preview))
		val := valueBuilder.String()
		// Discord field value max length is 1024
		if len(val) > 1024 { val = val[:1021] + "..." }
		fields = append(fields, &discordgo.MessageEmbedField{Name: name, Value: val, Inline: true})
	}

	embed := &discordgo.MessageEmbed{
		Title:       "Scheduled Says (next 20)",
		Description: fmt.Sprintf("Total queued (showing up to 20): %d", len(list)),
		Color:       utils.Colors.Info(),
		Fields:      fields,
		Footer: &discordgo.MessageEmbedFooter{Text: "Use /cancelscheduledsay <ID> to cancel"},
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{embed}, Flags: discordgo.MessageFlagsEphemeral}})
}

// handleCancelScheduledSay cancels a scheduled message by ID
func (h *SlashCommandHandler) handleCancelScheduledSay(s *discordgo.Session, i *discordgo.InteractionCreate) {
	var idVal int64
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "id" {
			idVal = opt.IntValue()
		}
	}
	if idVal == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "Missing or invalid ID.", Flags: discordgo.MessageFlagsEphemeral}})
		return
	}
	if h.ScheduleSayService.Cancel(idVal) {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: fmt.Sprintf("Cancelled scheduled say %d", idVal), Flags: discordgo.MessageFlagsEphemeral}})
	} else {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: fmt.Sprintf("No scheduled say with ID %d found", idVal), Flags: discordgo.MessageFlagsEphemeral}})
	}
}
