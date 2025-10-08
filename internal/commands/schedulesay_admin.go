package commands

import (
    "fmt"
    "strings"

    "github.com/bwmarrin/discordgo"
)

// handleListScheduledSays lists up to the next 20 scheduled messages
func (h *SlashCommandHandler) handleListScheduledSays(s *discordgo.Session, i *discordgo.InteractionCreate) {
    list := h.ScheduleSayService.List(20)
    if len(list) == 0 {
        _ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: "No scheduled messages.", Flags: discordgo.MessageFlagsEphemeral}})
        return
    }

    var b strings.Builder
    b.WriteString("ID | Fire (abs) | In | Channel | Preview | Suppress\n")
    for _, m := range list {
        preview := m.Content
        if len(preview) > 10 {
            preview = preview[:10]
        }
        b.WriteString(fmt.Sprintf("%d | <t:%d:F> | <t:%d:R> | %s | %.10q | %v\n", m.ID, m.FireAt.Unix(), m.FireAt.Unix(), m.ChannelID, preview, m.SuppressModMessage))
    }
    content := b.String()
    if len(content) > 1800 { // safety trimming
        content = content[:1800] + "..." 
    }
    _ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: fmt.Sprintf("```%s```", content), Flags: discordgo.MessageFlagsEphemeral}})
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
