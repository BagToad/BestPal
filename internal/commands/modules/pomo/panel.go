package pomo

import (
	"fmt"

	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// Button customID constants
const (
	buttonStart = "pomo::start"
	buttonStop  = "pomo::stop"
	buttonReset = "pomo::reset"
)

// Phase represents the current phase of the pomodoro timer
type Phase int

const (
	PhaseIdle     Phase = iota // Panel created, not yet started
	PhaseWorking               // 25-minute work session
	PhaseBreak                 // 5-minute break
	PhaseComplete              // All cycles finished
	PhasePaused                // Timer paused by user
)

const (
	WorkDuration  = 4 // minutes
	BreakDuration = 2 // minutes
	MaxPomos      = 10
)

// panelEmbed builds the embed for the pomo panel based on current state
func panelEmbed(phase Phase, minutesLeft int, currentPomo int, totalPomos int) *discordgo.MessageEmbed {
	switch phase {
	case PhaseIdle:
		return idleEmbed()
	case PhaseWorking:
		return workingEmbed(minutesLeft, currentPomo, totalPomos)
	case PhaseBreak:
		return breakEmbed(minutesLeft, currentPomo, totalPomos)
	case PhasePaused:
		return pausedEmbed(minutesLeft, currentPomo, totalPomos)
	case PhaseComplete:
		return completeEmbed(totalPomos)
	default:
		return idleEmbed()
	}
}

func idleEmbed() *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🍅 Pomodoro Timer",
		Description: "Press **Start** to begin a pomodoro session!\n\n`25 min work` → `5 min break` → repeat",
		Color:       utils.Colors.Info(),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Status", Value: "⏸️ Ready", Inline: true},
			{Name: "Session", Value: fmt.Sprintf("0 / %d", MaxPomos), Inline: true},
		},
	}
}

func workingEmbed(minutesLeft int, currentPomo int, totalPomos int) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🍅 Pomodoro Timer",
		Description: fmt.Sprintf("**Focus time!** Stay on task. 💪\n\n⏳ **%d min** remaining", minutesLeft),
		Color:       0xE74C3C, // Red for work
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Status", Value: "🔴 Working", Inline: true},
			{Name: "Session", Value: fmt.Sprintf("%d / %d", currentPomo, totalPomos), Inline: true},
			{Name: "Phase", Value: progressBar(WorkDuration-minutesLeft, WorkDuration), Inline: false},
		},
	}
}

func breakEmbed(minutesLeft int, currentPomo int, totalPomos int) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🍅 Pomodoro Timer",
		Description: fmt.Sprintf("**Break time!** Relax and recharge. ☕\n\n⏳ **%d min** remaining", minutesLeft),
		Color:       0x2ECC71, // Green for break
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Status", Value: "🟢 Break", Inline: true},
			{Name: "Session", Value: fmt.Sprintf("%d / %d", currentPomo, totalPomos), Inline: true},
			{Name: "Phase", Value: progressBar(BreakDuration-minutesLeft, BreakDuration), Inline: false},
		},
	}
}

func pausedEmbed(minutesLeft int, currentPomo int, totalPomos int) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🍅 Pomodoro Timer",
		Description: fmt.Sprintf("**Paused.** Press **Start** to resume.\n\n⏳ **%d min** were remaining", minutesLeft),
		Color:       utils.Colors.Warning(),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Status", Value: "⏸️ Paused", Inline: true},
			{Name: "Session", Value: fmt.Sprintf("%d / %d", currentPomo, totalPomos), Inline: true},
		},
	}
}

func completeEmbed(totalPomos int) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🍅 Pomodoro Timer",
		Description: fmt.Sprintf("🎉 **All %d pomodoros complete!** Great work! 🏆", totalPomos),
		Color:       utils.Colors.Ok(),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Status", Value: "✅ Complete", Inline: true},
			{Name: "Sessions", Value: fmt.Sprintf("%d / %d", totalPomos, totalPomos), Inline: true},
		},
	}
}

// progressBar renders a text-based progress bar
func progressBar(elapsed int, total int) string {
	const barLen = 10
	filled := 0
	if total > 0 {
		filled = (elapsed * barLen) / total
	}
	if filled > barLen {
		filled = barLen
	}
	bar := ""
	for j := 0; j < barLen; j++ {
		if j < filled {
			bar += "▓"
		} else {
			bar += "░"
		}
	}
	return fmt.Sprintf("`%s` %d/%d min", bar, elapsed, total)
}

// panelButtons returns the action row of buttons appropriate for the current phase
func panelButtons(phase Phase) []discordgo.MessageComponent {
	switch phase {
	case PhaseIdle:
		return []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				&discordgo.Button{Style: discordgo.SuccessButton, Label: "Start", CustomID: buttonStart, Emoji: &discordgo.ComponentEmoji{Name: "▶️"}},
			}},
		}
	case PhaseWorking, PhaseBreak:
		return []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				&discordgo.Button{Style: discordgo.DangerButton, Label: "Stop", CustomID: buttonStop, Emoji: &discordgo.ComponentEmoji{Name: "⏸️"}},
				&discordgo.Button{Style: discordgo.SecondaryButton, Label: "Reset", CustomID: buttonReset, Emoji: &discordgo.ComponentEmoji{Name: "🔄"}},
			}},
		}
	case PhasePaused:
		return []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				&discordgo.Button{Style: discordgo.SuccessButton, Label: "Start", CustomID: buttonStart, Emoji: &discordgo.ComponentEmoji{Name: "▶️"}},
				&discordgo.Button{Style: discordgo.SecondaryButton, Label: "Reset", CustomID: buttonReset, Emoji: &discordgo.ComponentEmoji{Name: "🔄"}},
			}},
		}
	case PhaseComplete:
		return []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				&discordgo.Button{Style: discordgo.SecondaryButton, Label: "Reset", CustomID: buttonReset, Emoji: &discordgo.ComponentEmoji{Name: "🔄"}},
			}},
		}
	default:
		return nil
	}
}
