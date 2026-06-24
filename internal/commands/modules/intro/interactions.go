package intro

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

const lookupGameThreadsCustomID = "intro:lookup-games"

type lookupGameThreadsAgentResult struct {
	GameThreads []lookupGameThreadResult `json:"game-threads"`
	Note        string                   `json:"note,omitempty"`
}

type lookupGameThreadResult struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Status string `json:"status"`
}

// HandleComponent routes component interactions for the intro module.
func (m *Module) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil {
		return
	}

	cid := i.MessageComponentData().CustomID
	if strings.HasPrefix(cid, lookupGameThreadsCustomID) {
		m.handleLookupGamesComponent(s, i)
	}
}

func (m *Module) handleLookupGamesComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := ""
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}
	if userID == "" {
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Could not determine user identity for this lookup.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}
	if i.ChannelID == "" {
		_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Could not determine the intro thread channel.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	_ = introRespond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	})

	clearComponents := []discordgo.MessageComponent{}
	if m.config == nil || m.config.Agent == nil {
		msg := "❌ Lookup agent is unavailable."
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{Content: &msg, Components: &clearComponents})
		return
	}

	prompt := fmt.Sprintf("Find the game threads for the games <@%s> plays.", userID)
	jsonReply := m.config.Agent.HandleInternal(s, prompt)
	if strings.TrimSpace(jsonReply) == "" {
		msg := "❌ Failed to look up game threads right now. Please try again."
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{Content: &msg, Components: &clearComponents})
		return
	}

	var result lookupGameThreadsAgentResult
	if err := json.Unmarshal([]byte(jsonReply), &result); err != nil {
		msg := "❌ The lookup response was not valid JSON."
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{Content: &msg, Components: &clearComponents})
		return
	}

	var b strings.Builder
	b.WriteString("**Game Threads:**")
	if len(result.GameThreads) == 0 {
		b.WriteString("\n- No matching game threads found.")
	} else {
		missing := false
		for _, thread := range result.GameThreads {
			name := strings.TrimSpace(thread.Name)
			url := strings.TrimSpace(thread.URL)
			status := strings.TrimSpace(strings.ToLower(thread.Status))
			if name == "" {
				continue
			}
			if status == "not found" || url == "" {
				missing = true
				b.WriteString(fmt.Sprintf("\n- **%s**: _not found_", name))
				continue
			}
			b.WriteString(fmt.Sprintf("\n- **%s**: %s", name, url))
		}
		if missing {
			note := strings.TrimSpace(result.Note)
			if note == "" {
				note = "ℹ️ Missing a thread? Create one in #create-a-thread."
			}
			b.WriteString("\n\n")
			b.WriteString(note)
		}
	}

	msg := b.String()
	_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{Content: &msg, Components: &clearComponents})
}
