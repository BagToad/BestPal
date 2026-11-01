package trickortreat

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"sort"
	"strconv"

	"gamerpal/internal/database"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// souvenirOrder is the fixed column order for known souvenir types in the
// CSV export. Unknown types found in the database get columns after these.
var souvenirOrder = []string{
	souvenirEggshell,
	souvenirTPRoll,
	souvenirToothbrush,
	souvenirTornFabric,
	souvenirGhostlyWrapper,
}

// memberPageSize is the most members Discord returns per list request.
const memberPageSize = 1000

// exportMember is how a user appears in the export's name columns.
type exportMember struct {
	Username    string
	DisplayName string
}

// sanitizeCell neutralizes spreadsheet formula injection.
func sanitizeCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r', '\n':
		return "'" + s
	}
	return s
}

// buildExportCSV renders one row per user: rank, identity, candies, one
// column per souvenir type, then the rest of their stored state. When
// membersComplete is false, users missing from members get in_server=unknown.
func buildExportCSV(rows []database.CandyExportRow, members map[string]exportMember, membersComplete bool) ([]byte, error) {
	types := append([]string{}, souvenirOrder...)
	known := map[string]bool{}
	for _, t := range types {
		known[t] = true
	}
	var extra []string
	for _, r := range rows {
		for t := range r.Souvenirs {
			if !known[t] {
				known[t] = true
				extra = append(extra, t)
			}
		}
	}
	sort.Strings(extra)
	types = append(types, extra...)

	header := []string{"rank", "user_id", "username", "display_name", "in_server", "candies"}
	for _, t := range types {
		name := t
		if info, ok := souvenirs[t]; ok {
			name = info.name
		}
		header = append(header, sanitizeCell(name))
	}
	header = append(header, "total_souvenirs", "treats_claimed", "tricks_played", "bowls_timed_out_of",
		"pending_bonus_candy", "bonus_source", "timeout_charges_left", "timeout_source", "first_seen_utc")

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(header); err != nil {
		return nil, err
	}
	for _, r := range rows {
		mem, inServer := members[r.UserID]
		in := "no"
		switch {
		case inServer:
			in = "yes"
		case !membersComplete:
			in = "unknown"
		}
		rec := []string{strconv.Itoa(r.Rank), r.UserID, sanitizeCell(mem.Username), sanitizeCell(mem.DisplayName), in,
			strconv.FormatInt(r.Candies, 10)}
		var total int64
		for _, t := range types {
			rec = append(rec, strconv.FormatInt(r.Souvenirs[t], 10))
			total += r.Souvenirs[t]
		}
		first := ""
		if !r.FirstSeen.IsZero() {
			first = r.FirstSeen.UTC().Format("2006-01-02 15:04:05")
		}
		rec = append(rec, strconv.FormatInt(total, 10),
			strconv.FormatInt(r.TreatsClaimed, 10), strconv.FormatInt(r.TricksPlayed, 10), strconv.FormatInt(r.TimeoutsServed, 10),
			strconv.Itoa(r.BonusCandy), sanitizeCell(r.BonusSource),
			strconv.Itoa(r.TimeoutCharges), sanitizeCell(r.TimeoutSource), first)
		if err := w.Write(rec); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// guildMembers maps the guild's current member IDs to their names. It pages
// through the member list until it has seen every wanted ID or runs out.
func (m *Module) guildMembers(s *discordgo.Session, guildID string, want map[string]bool) (map[string]exportMember, error) {
	out := map[string]exportMember{}
	after := ""
	for len(out) < len(want) {
		page, err := m.ops.GuildMembers(s, guildID, after, memberPageSize)
		if err != nil {
			return out, err
		}
		for _, mem := range page {
			if mem.User == nil {
				continue
			}
			if want[mem.User.ID] {
				out[mem.User.ID] = exportMember{Username: mem.User.Username, DisplayName: memberName(mem)}
			}
			after = mem.User.ID
		}
		if len(page) < memberPageSize {
			break
		}
	}
	return out, nil
}

func (m *Module) handleExport(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Checked here as well as in DefaultMemberPermissions, which server
	// owners can override per role.
	if !utils.HasBanPermissions(i) {
		m.respondEphemeral(s, i, "❌ You must be a moderator to use this command.")
		return
	}
	if m.store == nil {
		m.respondEphemeral(s, i, "❌ Database is unavailable.")
		return
	}
	if err := m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	}); err != nil {
		m.config.Logger.Errorf("trick-or-treat: failed to defer /candy-export: %v", err)
		return
	}
	edit := func(content string, files ...*discordgo.File) {
		if err := m.ops.EditResponse(s, i.Interaction, &discordgo.WebhookEdit{
			Content:         &content,
			Files:           files,
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		}); err != nil {
			m.config.Logger.Errorf("trick-or-treat: failed to send /candy-export: %v", err)
		}
	}

	rows, err := m.store.CandyExport(i.GuildID)
	if err != nil {
		m.config.Logger.Errorf("trick-or-treat: failed to load candy export: %v", err)
		edit("❌ Failed to load trick-or-treat data.")
		return
	}
	if len(rows) == 0 {
		edit("🍬 Nobody has any trick-or-treat data yet.")
		return
	}
	want := make(map[string]bool, len(rows))
	for _, r := range rows {
		want[r.UserID] = true
	}
	members, err := m.guildMembers(s, i.GuildID, want)
	note := ""
	if err != nil {
		m.config.Logger.Warnf("trick-or-treat: candy export member lookup failed: %v", err)
		note = "\n⚠️ Couldn't load the full member list, so some names are blank and in_server is `unknown` for anyone not found."
	}
	data, err := buildExportCSV(rows, members, err == nil)
	if err != nil {
		m.config.Logger.Errorf("trick-or-treat: failed to build candy export: %v", err)
		edit("❌ Failed to build the CSV.")
		return
	}
	name := fmt.Sprintf("candy-export-%s.csv", m.now().UTC().Format("20060102-150405"))
	people := "people"
	if len(rows) == 1 {
		people = "person"
	}
	edit(fmt.Sprintf("🎃 Trick-or-treat export: %d %s.%s", len(rows), people, note),
		&discordgo.File{Name: name, ContentType: "text/csv", Reader: bytes.NewReader(data)})
}
