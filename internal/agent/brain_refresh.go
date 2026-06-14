package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
)

// brainFetchPageLimit is the per-call ChannelMessages page size.
const brainFetchPageLimit = 100

// RefreshBrain reloads moderator guidance from the configured brain channel and
// swaps it into the in-memory Brain. It is safe to call concurrently from the
// scheduler and the startup load; refreshes are serialized. On any failure the
// previously loaded guidance is left untouched.
//
// Safe defaults: with no brain channel configured the guidance is cleared, so
// the agent behaves exactly as it does without the feature. When the channel
// cannot be read or is not private, the last known good guidance is kept.
func (a *Agent) RefreshBrain(ctx context.Context) error {
	if a == nil {
		return fmt.Errorf("nil agent")
	}
	a.brainRefreshMu.Lock()
	defer a.brainRefreshMu.Unlock()

	channelID := a.cfg.GetCopilotAgentBrainChannelID()
	if channelID == "" {
		a.brain.set("")
		return nil
	}

	guildID := a.cfg.GetGamerPalsServerID()

	// Privacy gate: refuse to load unless @everyone is denied View Channel. A
	// public brain channel would let anyone write into Lilly's prompt, so this
	// is the main injection control.
	ch, err := a.session.Channel(channelID)
	if err != nil {
		return fmt.Errorf("read brain channel: %w", err)
	}
	if !channelHiddenFromEveryone(ch, guildID) {
		a.cfg.Logger.Warnf("agent: brain channel %s is not private (@everyone can view); skipping load", channelID)
		return fmt.Errorf("brain channel is not private; refusing to load")
	}

	maxItems := a.cfg.GetCopilotAgentBrainMaxItems()
	maxChars := a.cfg.GetCopilotAgentBrainMaxChars()
	msgs, err := a.fetchBrainMessages(ctx, channelID, maxItems)
	if err != nil {
		return fmt.Errorf("fetch brain messages: %w", err)
	}

	guidance, _, _ := messagesToGuidance(msgs, maxItems, maxChars)
	a.brain.set(guidance)
	return nil
}

// brainRefreshTimeout bounds a single scheduled brain refresh so a slow or hung
// Discord call cannot tie up the scheduler worker indefinitely.
const brainRefreshTimeout = 30 * time.Second

// ScheduledFuncs returns the agent's recurring tasks keyed by cron schedule, so
// the agent module can register them through the standard module scheduler.
//
// The brain refresh warn-logs locally and returns nil on purpose: the scheduler
// reports returned errors to the mod log channel, and a persistently
// misconfigured channel would otherwise spam that channel every interval.
func (a *Agent) ScheduledFuncs() map[string]func() error {
	if a == nil {
		return nil
	}
	schedule := fmt.Sprintf("@every %s", a.cfg.GetCopilotAgentBrainRefreshInterval())
	return map[string]func() error{
		schedule: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), brainRefreshTimeout)
			defer cancel()
			if err := a.RefreshBrain(ctx); err != nil {
				a.cfg.Logger.Warnf("agent brain refresh: %v", err)
			}
			return nil
		},
	}
}

// PreloadBrain performs a best-effort initial brain load in the background so
// guidance is present shortly after startup, without waiting for the first
// scheduled refresh (which fires one interval later). It never blocks the
// caller; on failure the next scheduled refresh recovers. Refreshes are
// serialized in RefreshBrain, so this is safe alongside the scheduled job.
func (a *Agent) PreloadBrain() {
	if a == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), brainRefreshTimeout)
		defer cancel()
		if err := a.RefreshBrain(ctx); err != nil {
			a.cfg.Logger.Warnf("agent brain initial load failed: %v", err)
		} else {
			a.cfg.Logger.Infof("agent brain initial load complete")
		}
	}()
}

// fetchBrainMessages pages through the brain channel newest-first, stopping once
// it has enough messages to satisfy the guidance cap (with headroom for
// filtered-out bot/empty messages) or the channel is exhausted.
func (a *Agent) fetchBrainMessages(ctx context.Context, channelID string, maxItems int) ([]*discordgo.Message, error) {
	var all []*discordgo.Message
	beforeID := ""
	// Fetch a few extra pages beyond the item cap so bot/empty messages that
	// get filtered out do not starve the guidance.
	maxFetch := maxItems * 3
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := a.session.ChannelMessages(channelID, brainFetchPageLimit, beforeID, "", "")
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		all = append(all, batch...)
		if len(all) >= maxFetch || len(batch) < brainFetchPageLimit {
			break
		}
		beforeID = batch[len(batch)-1].ID
		time.Sleep(100 * time.Millisecond)
	}
	return all, nil
}
