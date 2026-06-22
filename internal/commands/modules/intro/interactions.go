package intro

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

const lookupGameThreadsCustomID = "intro:lookup-games"

type lookupGameThreadsAgentResult struct {
	Games []lookupGameThreadResult `json:"games"`

	MissingGames []string `json:"missing_games"`
	Note         string   `json:"note,omitempty"`
}

type lookupGameThreadResult struct {
	GameName string `json:"game_name"`
	Thread   struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"thread"`
}

// HandleComponent routes component interactions for the intro module.
func (m *Module) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.MessageComponentData() == nil {
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
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: 0,
		},
	})

	if m.config == nil || m.config.Agent == nil {
		msg := "❌ Lookup agent is unavailable."
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	prompt := fmt.Sprintf("Find the game threads for the games <@%s> plays.", userID)
	jsonReply, err := m.config.Agent.HandleComponent(s, i, prompt)
	if err != nil {
		msg := "❌ Failed to look up game threads right now. Please try again."
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	var result lookupGameThreadsAgentResult
	if err := json.Unmarshal([]byte(jsonReply), &result); err != nil {
		msg := "❌ The lookup response was not valid JSON."
		_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	markdown := buildLookupGameThreadsMarkdown(result)
	_, _ = introEdit(s, i.Interaction, &discordgo.WebhookEdit{Content: &markdown})
}

func buildLookupGameThreadsMarkdown(result lookupGameThreadsAgentResult) string {
	var b strings.Builder
	b.WriteString("**Game Threads:**")

	if len(result.Games) == 0 {
		b.WriteString("\n- No matching game threads found.")
	} else {
		for _, g := range result.Games {
			name := strings.TrimSpace(g.GameName)
			url := strings.TrimSpace(g.Thread.URL)
			threadName := strings.TrimSpace(g.Thread.Name)
			if name == "" {
				name = "Unknown game"
			}
			if url == "" {
				b.WriteString(fmt.Sprintf("\n- **%s**", name))
				continue
			}
			if threadName == "" {
				b.WriteString(fmt.Sprintf("\n- **%s**: %s", name, url))
				continue
			}
			b.WriteString(fmt.Sprintf("\n- **%s**: [%s](%s)", name, threadName, url))
		}
	}

	if len(result.MissingGames) > 0 {
		note := strings.TrimSpace(result.Note)
		if note == "" {
			note = "ℹ️ Missing a thread? Create one in #create-a-thread."
		}
		b.WriteString("\n\n")
		b.WriteString(note)
	}

	return b.String()
}
