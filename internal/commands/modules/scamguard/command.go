package scamguard

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
)

// markResult summarizes a "Mark as Scam Image" operation.
type markResult struct {
	images int // image attachments seen
	added  int // hashes newly added
	known  int // hashes already present
	failed int // attachments that could not be fetched/hashed
}

// markMessageScam hashes every image attachment on msg and adds each hash to
// the blocklist. It performs no Discord I/O so it can be unit-tested directly.
func (m *Module) markMessageScam(msg *discordgo.Message, addedBy string) markResult {
	var res markResult
	for _, a := range msg.Attachments {
		if a == nil || !isImageAttachment(a) {
			continue
		}
		res.images++
		if a.Size > maxImageBytes {
			res.failed++
			continue
		}
		data, err := m.fetchImage(a.URL, maxImageBytes)
		if err != nil {
			m.config.Logger.Warnf("scamguard: mark failed to fetch %q: %v", a.URL, err)
			res.failed++
			continue
		}
		h, err := computeHash(data)
		if err != nil {
			m.config.Logger.Warnf("scamguard: mark failed to hash %q: %v", a.URL, err)
			res.failed++
			continue
		}
		added, err := m.addKnownHash(hashString(h), a.Filename, addedBy, "command")
		if err != nil {
			m.config.Logger.Warnf("scamguard: mark failed to store hash: %v", err)
			res.failed++
			continue
		}
		if added {
			res.added++
		} else {
			res.known++
		}
	}
	return res
}

// handleMarkScam is the handler for the "Mark as Scam Image" context-menu
// command. It hashes the target message's image attachments, adds them to the
// blocklist, deletes the target message, and replies ephemerally.
func (m *Module) handleMarkScam(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	data := i.ApplicationCommandData()
	var msg *discordgo.Message
	if data.Resolved != nil {
		msg = data.Resolved.Messages[data.TargetID]
	}
	if msg == nil {
		m.editResponse(s, i, "Could not resolve the target message.")
		return
	}

	addedBy := ""
	if i.Member != nil && i.Member.User != nil {
		addedBy = i.Member.User.ID
	}

	// Hash and blocklist every image on the message. A scam message may carry
	// more than one image, so each is checked and added independently.
	res := m.markMessageScam(msg, addedBy)
	if res.images == 0 {
		m.editResponse(s, i, "That message has no image attachments to mark.")
		return
	}

	// Delete the target message so the sample stops spreading.
	if err := m.deleteMessage(s, msg.ChannelID, msg.ID); err != nil {
		m.config.Logger.Warnf("scamguard: failed to delete marked message %s: %v", msg.ID, err)
	}

	m.editResponse(s, i, fmt.Sprintf(
		"Marked as scam: %d added, %d already known, %d failed (of %d image(s)). Message deleted.",
		res.added, res.known, res.failed, res.images,
	))
}

// editResponse edits the deferred ephemeral response with content.
func (m *Module) editResponse(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content})
}

// unmarkResult summarizes an "Unmark Scam Image" operation.
type unmarkResult struct {
	images  int      // image attachments seen
	removed int      // blocklist entries removed
	missed  int      // images with no matching blocklist entry
	failed  int      // attachments that could not be fetched/hashed
	hashes  []string // hash strings that were removed
}

// unmarkMessageScam hashes every image attachment on msg and removes any
// blocklist entry within the configured Hamming distance of it. Removal is
// distance-based (not exact) so a re-uploaded copy of a wrongly-blocklisted
// image still clears the offending entry. It performs no Discord I/O so it can
// be unit-tested directly.
func (m *Module) unmarkMessageScam(msg *discordgo.Message) unmarkResult {
	threshold := m.config.GetScamGuardHashThreshold()
	var res unmarkResult
	for _, a := range msg.Attachments {
		if a == nil || !isImageAttachment(a) {
			continue
		}
		res.images++
		if a.Size > maxImageBytes {
			res.failed++
			continue
		}
		data, err := m.fetchImage(a.URL, maxImageBytes)
		if err != nil {
			m.config.Logger.Warnf("scamguard: unmark failed to fetch %q: %v", a.URL, err)
			res.failed++
			continue
		}
		h, err := computeHash(data)
		if err != nil {
			m.config.Logger.Warnf("scamguard: unmark failed to hash %q: %v", a.URL, err)
			res.failed++
			continue
		}
		matches := m.matchingHashes(h, threshold)
		if len(matches) == 0 {
			res.missed++
			continue
		}
		for _, hs := range matches {
			removed, err := m.removeKnownHash(hs)
			if err != nil {
				m.config.Logger.Warnf("scamguard: unmark failed to remove hash: %v", err)
				res.failed++
				continue
			}
			if removed {
				res.removed++
				res.hashes = append(res.hashes, hs)
			}
		}
	}
	return res
}

// handleUnmarkScam is the handler for the "Unmark Scam Image" context-menu
// command. It removes any blocklist entry matching the target message's images,
// recovering from a false positive (an image marked by mistake that is now
// auto-actioning innocent users). The target message is left in place.
func (m *Module) handleUnmarkScam(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	data := i.ApplicationCommandData()
	var msg *discordgo.Message
	if data.Resolved != nil {
		msg = data.Resolved.Messages[data.TargetID]
	}
	if msg == nil {
		m.editResponse(s, i, "Could not resolve the target message.")
		return
	}

	res := m.unmarkMessageScam(msg)
	if res.images == 0 {
		m.editResponse(s, i, "That message has no image attachments to unmark.")
		return
	}

	if res.removed > 0 {
		removedBy := ""
		if i.Member != nil && i.Member.User != nil {
			removedBy = i.Member.User.ID
		}
		m.logUnmark(s, removedBy, res)
	}

	m.editResponse(s, i, fmt.Sprintf(
		"Unmarked: removed %d blocklist hash(es); %d image(s) had no match; %d failed (of %d image(s)).",
		res.removed, res.missed, res.failed, res.images,
	))
}
