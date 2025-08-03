package scheduler

import (
	"fmt"
	"gamerpal/internal/database"
	"time"
)

// checkAndExecuteScheduledPairings checks for due scheduled pairings and executes them
func (s *Scheduler) checkAndExecuteScheduledPairings() {
	scheduledPairings, err := s.db.GetScheduledPairings()
	if err != nil {
		s.config.Logger.Errorf("Error getting scheduled pairings: %v", err)
		return
	}

	if len(scheduledPairings) == 0 {
		return // No scheduled pairings
	}

	s.config.Logger.Infof("Found %d scheduled pairing(s) to execute", len(scheduledPairings))

	for _, schedule := range scheduledPairings {
		s.executeScheduledPairing(schedule)
	}
}

// executeScheduledPairing executes a single scheduled pairing
func (s *Scheduler) executeScheduledPairing(schedule database.RouletteSchedule) {
	s.config.Logger.Infof("Executing scheduled pairing for guild %s (scheduled for %s)",
		schedule.GuildID, schedule.ScheduledAt.Format("2006-01-02 15:04:05"))

	// Execute pairing using the pairing service
	result, err := s.pairingService.ExecutePairing(schedule.GuildID, false)
	if err != nil {
		s.config.Logger.Errorf("Error executing scheduled pairing for guild %s: %v", schedule.GuildID, err)
		s.notifyFailedPairing(schedule.GuildID, fmt.Sprintf("Error executing pairing: %v", err))
		return
	}

	if !result.Success {
		s.config.Logger.Errorf("Scheduled pairing failed for guild %s: %s", schedule.GuildID, result.ErrorMessage)
		s.notifyFailedPairing(schedule.GuildID, result.ErrorMessage)
	} else {
		s.config.Logger.Infof("Successfully executed scheduled pairing for guild %s: %d pair(s) created",
			schedule.GuildID, result.PairCount)

		// Log the results
		s.pairingService.LogPairingResults(schedule.GuildID, result, true)
	}
}

// notifyFailedPairing sends a notification about failed automated pairing
func (s *Scheduler) notifyFailedPairing(guildID, reason string) {
	modLogChannelID := s.config.GetGamerPalsModActionLogChannelID()
	if modLogChannelID == "" {
		return
	}

	message := fmt.Sprintf("⚠️ **Automated Roulette Pairing Failed**\n\n"+
		"**Guild:** %s\n"+
		"**Reason:** %s\n"+
		"**Time:** <t:%d:F>\n\n"+
		"Please check the roulette system and manually trigger pairing if needed.",
		guildID, reason, time.Now().Unix())

	_, err := s.session.ChannelMessageSend(modLogChannelID, message)
	if err != nil {
		s.config.Logger.Errorf("Error sending failed pairing notification: %v", err)
	}
}
