package intro

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

const lookupGameThreadsCustomID = "intro:lookup-games"

type gameThreadsAgentResult struct {
	GameThreads []gameThread `json:"game-threads"`
}

type gameThread struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Status string `json:"status"`
}

type gameThreadsMessage struct {
	Preamble string
	Result   []gameThread
	Note     string
}

func (m gameThreadsMessage) String() string {
	var b strings.Builder
	b.WriteString("**Game Threads:**")
	if len(m.Result) == 0 {
		b.WriteString("\n- No matching game threads found.")
	} else {
		missing := false
		for _, thread := range m.Result {
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
			note := strings.TrimSpace(m.Note)
			if note == "" {
				note = "ℹ️ Missing a thread? Create one in #create-a-thread."
			}
			b.WriteString("\n\n")
			b.WriteString(note)
		}
	}

	section := b.String()
	trimmedPreamble := strings.TrimSpace(m.Preamble)
	if trimmedPreamble == "" {
		return section
	}
	return trimmedPreamble + "\n\n---\n\n" + section
}

func (m gameThreadsMessage) BuildWithError(errMsg string) string {
	trimmedPreamble := strings.TrimSpace(m.Preamble)
	trimmedErr := strings.TrimSpace(errMsg)
	if trimmedPreamble == "" {
		return trimmedErr
	}
	if trimmedErr == "" {
		return trimmedPreamble
	}
	return trimmedPreamble + "\n\n---\n\n" + trimmedErr
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

	msgObj := gameThreadsMessage{}
	if i.Message != nil {
		msgObj.Preamble = i.Message.Content
	}

	clearComponents := []discordgo.MessageComponent{}
	if m.config == nil || m.config.Agent == nil {
		msg := msgObj.BuildWithError("❌ Lookup agent is unavailable.")
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{Content: &msg, Components: &clearComponents})
		return
	}

	prompt := fmt.Sprintf("Find the game threads for the games <@%s> plays.", userID)
	jsonReply := m.config.Agent.HandleInternal(s, prompt)
	if strings.TrimSpace(jsonReply) == "" {
		msg := msgObj.BuildWithError("❌ Failed to look up game threads right now. Please try again.")
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{Content: &msg, Components: &clearComponents})
		return
	}

	var agentResult gameThreadsAgentResult
	if err := json.Unmarshal([]byte(jsonReply), &agentResult); err != nil {
		msg := msgObj.BuildWithError("❌ The lookup response was not valid JSON.")
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{Content: &msg, Components: &clearComponents})
		return
	}

	msgObj.Result = agentResult.GameThreads
	msg := msgObj.String()
	_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{Content: &msg, Components: &clearComponents})
}
