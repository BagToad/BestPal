package trickortreat

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"testing"

	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func exportCmd(perms int64) *discordgo.InteractionCreate {
	cmd := command("candy-export", "mod")
	cmd.Member.Permissions = perms
	return cmd
}

func readExport(t *testing.T, edit *discordgo.WebhookEdit) [][]string {
	t.Helper()
	require.Len(t, edit.Files, 1)
	raw, err := io.ReadAll(edit.Files[0].Reader)
	require.NoError(t, err)
	recs, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	require.NoError(t, err)
	return recs
}

func TestCandyExport_RequiresModerator(t *testing.T) {
	m, c, _ := newTestModule(t, nil)
	m.handleExport(nil, exportCmd(discordgo.PermissionManageMessages))
	assert.Equal(t, "❌ You must be a moderator to use this command.", lastResponse(t, c).Data.Content)
	assert.Equal(t, discordgo.MessageFlagsEphemeral, lastResponse(t, c).Data.Flags)
	assert.Empty(t, c.responseEdits)

	m.handleExport(nil, exportCmd(discordgo.PermissionAdministrator))
	require.Len(t, c.responseEdits, 1)
}

func TestCandyExport_MemberLookupFails(t *testing.T) {
	m, c, db := newTestModule(t, nil)
	_, _, err := db.AdjustCandies("guild1", "u1", 2)
	require.NoError(t, err)
	c.membersErr = errors.New("boom")
	m.handleExport(nil, exportCmd(discordgo.PermissionBanMembers))
	edit := c.responseEdits[len(c.responseEdits)-1]
	assert.Contains(t, *edit.Content, "in_server is `unknown`")
	recs := readExport(t, edit)
	require.Len(t, recs, 2)
	assert.Equal(t, []string{"1", "u1", "", "", "unknown", "2"}, recs[1][:6])
}

func TestBuildExportCSV_SanitizesUnknownSouvenirHeaders(t *testing.T) {
	raw, err := buildExportCSV([]database.CandyExportRow{
		{TOTUser: database.TOTUser{UserID: "u1"}, Rank: 1, Souvenirs: map[string]int64{"=1+1": 2}},
	}, nil, true)
	require.NoError(t, err)
	recs, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	require.NoError(t, err)
	assert.Contains(t, recs[0], "'=1+1")
	assert.NotContains(t, recs[0], "=1+1")
}

func TestCandyExport(t *testing.T) {
	m, c, db := newTestModule(t, nil)
	m.handleExport(nil, exportCmd(discordgo.PermissionBanMembers))
	require.Len(t, c.responseEdits, 1)
	assert.Equal(t, "🍬 Nobody has any trick-or-treat data yet.", *c.responseEdits[0].Content)
	assert.Equal(t, discordgo.MessageFlagsEphemeral, lastResponse(t, c).Data.Flags, "deferred privately")

	bowl := spawn(t, m)
	m.HandleComponent(nil, click(treatButtonID, bowl, "u1"))
	m.HandleComponent(nil, click(treatButtonID, bowl, "u2"))
	_, _, err := db.AdjustCandies("guild1", "u1", 4)
	require.NoError(t, err)
	_, _, err = db.DrainBowl(bowl, m.now(), trickWindow, 0, func(int) string { return "drained" })
	require.NoError(t, err)
	_, err = db.ApplyTrick(bowl, "u2", m.now(), func(tx database.TrickTx) (string, error) {
		if err := tx.AddSouvenir("u2", souvenirEggshell); err != nil {
			return "", err
		}
		if err := tx.AddSouvenir("u2", "mystery_goo"); err != nil {
			return "", err
		}
		_, err := tx.GrantBonus("u2", 3, bonusSourceFullSized)
		return "", err
	})
	require.NoError(t, err)

	c.members = []*discordgo.Member{
		{User: &discordgo.User{ID: "u1", Username: "one", GlobalName: "One"}, Nick: "=SUM(A1)"},
	}
	m.handleExport(nil, exportCmd(discordgo.PermissionBanMembers))
	edit := c.responseEdits[len(c.responseEdits)-1]
	assert.Equal(t, "🎃 Trick-or-treat export: 2 people.", *edit.Content)
	require.Len(t, edit.Files, 1)
	assert.Equal(t, "text/csv", edit.Files[0].ContentType)
	raw, err := io.ReadAll(edit.Files[0].Reader)
	require.NoError(t, err)
	recs, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	require.NoError(t, err)
	require.Len(t, recs, 3)
	assert.Equal(t, []string{"rank", "user_id", "username", "display_name", "in_server", "candies",
		"Eggshell", "TP Roll", "Toothbrush", "Torn Fabric", "Ghostly Wrapper", "mystery_goo",
		"total_souvenirs", "treats_claimed", "tricks_played", "bowls_timed_out_of",
		"pending_bonus_candy", "bonus_source", "timeout_charges_left", "timeout_source", "first_seen_utc"}, recs[0])
	assert.Equal(t, []string{"1", "u1", "one", "'=SUM(A1)", "yes", "5", "0", "0", "0", "0", "0", "0", "0", "1", "0", "0", "0", "", "0", ""}, recs[1][:20])
	assert.NotEmpty(t, recs[1][20], "first seen")
	assert.Equal(t, []string{"2", "u2", "", "", "no", "1", "1", "0", "0", "0", "0", "1", "2", "1", "1", "0", "3", "Full-Sized Bar House", "0", ""}, recs[2][:20])
}

func TestGuildMembers_Pages(t *testing.T) {
	m, c, _ := newTestModule(t, nil)
	for n := range memberPageSize + 5 {
		id := "m" + string(rune('a'+n/676)) + string(rune('a'+n/26%26)) + string(rune('a'+n%26))
		c.members = append(c.members, &discordgo.Member{User: &discordgo.User{ID: id, Username: id}})
	}
	last := c.members[len(c.members)-1].User.ID
	got, err := m.guildMembers(nil, "guild1", map[string]bool{last: true, "gone": true})
	require.NoError(t, err)
	assert.Equal(t, map[string]exportMember{last: {Username: last, DisplayName: last}}, got)
}
