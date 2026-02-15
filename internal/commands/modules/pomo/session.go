package pomo

import (
	"sync"
	"time"

	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
)

// sessions maps voice channel ID → active PomoSession
var sessions sync.Map

// PomoSession tracks the state of a pomodoro timer for a single voice channel
type PomoSession struct {
	mu sync.Mutex

	// Discord context
	guildID        string
	voiceChannelID string
	channelID      string // text channel where the panel message lives
	messageID      string // panel message ID for edits

	// Timer state
	phase       Phase
	minutesLeft int
	currentPomo int
	totalPomos  int

	// Control channels
	stopCh  chan struct{}
	resetCh chan struct{}
	done    chan struct{} // closed when the goroutine exits

	// Dependencies
	session *discordgo.Session
	config  *config.Config

	// Voice connection (managed outside the mutex)
	voiceConn *discordgo.VoiceConnection
}

// GetOrCreateSession returns an existing session for the voice channel, or creates a new one.
// Returns the session and true if it already existed.
func GetOrCreateSession(s *discordgo.Session, cfg *config.Config, guildID, voiceChannelID, channelID, messageID string) (*PomoSession, bool) {
	if existing, ok := sessions.Load(voiceChannelID); ok {
		ps := existing.(*PomoSession)
		// Update the message ID in case /pomo was re-run
		ps.mu.Lock()
		ps.messageID = messageID
		ps.channelID = channelID
		ps.mu.Unlock()
		return ps, true
	}

	ps := &PomoSession{
		guildID:        guildID,
		voiceChannelID: voiceChannelID,
		channelID:      channelID,
		messageID:      messageID,
		phase:          PhaseIdle,
		minutesLeft:    0,
		currentPomo:    0,
		totalPomos:     MaxPomos,
		stopCh:         make(chan struct{}, 1),
		resetCh:        make(chan struct{}, 1),
		done:           make(chan struct{}),
		session:        s,
		config:         cfg,
	}

	sessions.Store(voiceChannelID, ps)
	return ps, false
}

// GetSession returns an existing session for the voice channel, or nil.
func GetSession(voiceChannelID string) *PomoSession {
	if existing, ok := sessions.Load(voiceChannelID); ok {
		return existing.(*PomoSession)
	}
	return nil
}

// RemoveSession removes the session from the global map.
func RemoveSession(voiceChannelID string) {
	sessions.Delete(voiceChannelID)
}

// Start begins or resumes the pomodoro timer.
func (ps *PomoSession) Start() {
	ps.mu.Lock()

	switch ps.phase {
	case PhaseIdle:
		// Fresh start: begin first pomo
		ps.currentPomo = 1
		ps.minutesLeft = WorkDuration
		ps.phase = PhaseWorking
		ps.updatePanel()
		ps.mu.Unlock()

		// Join voice outside the lock (blocking network call)
		ps.joinVC()

		go ps.runTimer()

	case PhasePaused:
		// Resume from pause
		ps.phase = PhaseWorking
		ps.updatePanel()
		// Recreate control channels and restart the timer goroutine
		ps.stopCh = make(chan struct{}, 1)
		ps.resetCh = make(chan struct{}, 1)
		ps.done = make(chan struct{})
		ps.mu.Unlock()

		// Join voice outside the lock (blocking network call)
		ps.joinVC()

		go ps.runTimer()

	default:
		// Already running or complete, ignore
		ps.mu.Unlock()
	}
}

// Stop pauses the pomodoro timer.
func (ps *PomoSession) Stop() {
	ps.mu.Lock()
	if ps.phase != PhaseWorking && ps.phase != PhaseBreak {
		ps.mu.Unlock()
		return
	}
	ps.mu.Unlock()

	// Signal the timer goroutine to stop
	select {
	case ps.stopCh <- struct{}{}:
	default:
	}
	// Wait for the goroutine to exit
	<-ps.done

	// Leave voice outside the lock
	ps.leaveVC()
}

// Reset fully resets the session to idle state.
func (ps *PomoSession) Reset() {
	ps.mu.Lock()
	wasRunning := ps.phase == PhaseWorking || ps.phase == PhaseBreak
	ps.mu.Unlock()

	if wasRunning {
		// Signal the timer goroutine to reset
		select {
		case ps.resetCh <- struct{}{}:
		default:
		}
		// Wait for the goroutine to exit
		<-ps.done
	} else {
		// Not running, just reset state directly
		ps.mu.Lock()
		ps.phase = PhaseIdle
		ps.minutesLeft = 0
		ps.currentPomo = 0
		ps.updatePanel()
		ps.mu.Unlock()
	}

	// Leave voice outside the lock
	ps.leaveVC()
}

// runTimer is the main timer goroutine. It ticks every minute.
func (ps *PomoSession) runTimer() {
	defer close(ps.done)

	// Interval between panel updates (every 2 minutes to respect rate limits)
	const panelUpdateInterval = 2

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	minutesSinceUpdate := 0

	for {
		select {
		case <-ps.stopCh:
			ps.mu.Lock()
			ps.phase = PhasePaused
			ps.updatePanel()
			ps.mu.Unlock()
			return

		case <-ps.resetCh:
			ps.mu.Lock()
			ps.phase = PhaseIdle
			ps.minutesLeft = 0
			ps.currentPomo = 0
			ps.updatePanel()
			ps.mu.Unlock()
			return

		case <-ticker.C:
			ps.mu.Lock()
			ps.minutesLeft--
			minutesSinceUpdate++

			if ps.minutesLeft <= 0 {
				// Phase transition
				ps.handlePhaseTransition()
				minutesSinceUpdate = 0
			} else if minutesSinceUpdate >= panelUpdateInterval {
				// Periodic panel update
				ps.updatePanel()
				minutesSinceUpdate = 0
			}

			// Check if we're done (complete phase set by handlePhaseTransition)
			if ps.phase == PhaseComplete {
				ps.mu.Unlock()
				// Play final chime then leave voice
				ps.playThenLeave(SoundResumeChime)
				return
			}
			ps.mu.Unlock()
		}
	}
}

// handlePhaseTransition handles the end of a work or break phase.
// Must be called with ps.mu held.
func (ps *PomoSession) handlePhaseTransition() {
	switch ps.phase {
	case PhaseWorking:
		// Work phase ended → start break
		ps.minutesLeft = BreakDuration
		ps.phase = PhaseBreak
		ps.updatePanel()
		ps.playSoundAsync(SoundBreakBell)

	case PhaseBreak:
		// Break ended → check if we have more pomos
		if ps.currentPomo >= ps.totalPomos {
			// All done!
			ps.phase = PhaseComplete
			ps.minutesLeft = 0
			ps.updatePanel()
			// Voice cleanup handled by caller after unlock
		} else {
			// Start next pomo
			ps.currentPomo++
			ps.minutesLeft = WorkDuration
			ps.phase = PhaseWorking
			ps.updatePanel()
			ps.playSoundAsync(SoundResumeChime)
		}
	}
}

// updatePanel edits the panel message with the current state.
// Must be called with ps.mu held.
func (ps *PomoSession) updatePanel() {
	embed := panelEmbed(ps.phase, ps.minutesLeft, ps.currentPomo, ps.totalPomos)
	buttons := panelButtons(ps.phase)
	embeds := []*discordgo.MessageEmbed{embed}

	_, err := ps.session.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel:    ps.channelID,
		ID:         ps.messageID,
		Embeds:     &embeds,
		Components: &buttons,
	})
	if err != nil && ps.config != nil {
		ps.config.Logger.Errorf("Pomo: failed to update panel: %v", err)
	}
}

// State returns the current phase and timing info (thread-safe).
func (ps *PomoSession) State() (phase Phase, minutesLeft int, currentPomo int, totalPomos int) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.phase, ps.minutesLeft, ps.currentPomo, ps.totalPomos
}

// joinVC joins the voice channel. Called outside the mutex (blocking network call).
func (ps *PomoSession) joinVC() {
	if ps.voiceConn != nil {
		return
	}
	ps.config.Logger.Infof("Pomo: joining voice channel %s in guild %s", ps.voiceChannelID, ps.guildID)
	vc, err := joinVoice(ps.session, ps.guildID, ps.voiceChannelID)
	if err != nil {
		ps.config.Logger.Errorf("Pomo: failed to join voice: %v", err)
		return
	}
	ps.config.Logger.Infof("Pomo: voice joined successfully, Ready=%v", vc.Ready)
	ps.voiceConn = vc
}

// leaveVC disconnects from the voice channel. Called outside the mutex.
func (ps *PomoSession) leaveVC() {
	if ps.voiceConn != nil {
		leaveVoice(ps.voiceConn)
		ps.voiceConn = nil
	}
}

// playSoundAsync plays a sound in a goroutine (non-blocking, safe to call with mutex held).
func (ps *PomoSession) playSoundAsync(soundFile string) {
	vc := ps.voiceConn
	if vc != nil {
		vc.RLock()
		ready := vc.Ready
		vc.RUnlock()
		ps.config.Logger.Infof("Pomo: playing sound %s, vc.Ready=%v", soundFile, ready)
		if !ready {
			ps.config.Logger.Warnf("Pomo: voice not ready, skipping sound playback")
			return
		}
		go func() {
			if err := playSound(vc, soundFile); err != nil {
				ps.config.Logger.Errorf("Pomo: failed to play sound: %v", err)
			}
		}()
	}
}

// playThenLeave plays a sound then disconnects from voice. Called outside the mutex.
func (ps *PomoSession) playThenLeave(soundFile string) {
	vc := ps.voiceConn
	ps.voiceConn = nil
	if vc != nil {
		_ = playSound(vc, soundFile)
		leaveVoice(vc)
	}
}
