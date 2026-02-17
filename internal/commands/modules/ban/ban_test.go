package ban

import (
	"testing"

	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type banCapture struct {
	banCalls   []banCall
	edits      []string
	dmCalls    []dmCall
	modLogs    []*discordgo.MessageEmbed
	bestpalLog []string
}

type banCall struct {
	guildID string
	userID  string
	reason  string
	days    int
}

type dmCall struct {
	userID  string
	message string
}

func mockSession() *discordgo.Session {
	return &discordgo.Session{
		State: discordgo.NewState(),
	}
}

func testOpts(cap *banCapture) banOpts {
	return banOpts{
		CreateBan: func(_ *discordgo.Session, guildID, userID, reason string, days int) error {
			cap.banCalls = append(cap.banCalls, banCall{guildID: guildID, userID: userID, reason: reason, days: days})
			return nil
		},
		Respond: func(_ *discordgo.Session, _ *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
			return nil
		},
		EditResponse: func(_ *discordgo.Session, _ *discordgo.Interaction, edit *discordgo.WebhookEdit) error {
			if edit != nil && edit.Content != nil {
				cap.edits = append(cap.edits, *edit.Content)
			}
			return nil
		},
		SendDM: func(_ *discordgo.Session, userID, message string) error {
			cap.dmCalls = append(cap.dmCalls, dmCall{userID: userID, message: message})
			return nil
		},
		LogToChannel: func(_ *config.Config, _ *discordgo.Session, _ string, embed *discordgo.MessageEmbed) error {
			cap.modLogs = append(cap.modLogs, embed)
			return nil
		},
		LogToBestPal: func(_ *config.Config, _ *discordgo.Session, msg string) error {
			cap.bestpalLog = append(cap.bestpalLog, msg)
			return nil
		},
	}
}

func newModule(t *testing.T, cap *banCapture) *BanModule {
	t.Helper()
	cfg := config.NewMockConfig(map[string]interface{}{
		"gamerpals_mod_action_log_channel_id": "mod-log-chan",
		"gamerpals_log_channel_id":            "bestpal-log-chan",
	})
	return &BanModule{config: cfg, opts: testOpts(cap)}
}

func buildSlashInteraction(invokerID, targetID string, days *int, reason *string) *discordgo.InteractionCreate {
	opts := []*discordgo.ApplicationCommandInteractionDataOption{
		{
			Name:  "user",
			Type:  discordgo.ApplicationCommandOptionUser,
			Value: targetID,
		},
	}
	if days != nil {
		opts = append(opts, &discordgo.ApplicationCommandInteractionDataOption{
			Name:  "days",
			Type:  discordgo.ApplicationCommandOptionInteger,
			Value: float64(*days),
		})
	}
	if reason != nil {
		opts = append(opts, &discordgo.ApplicationCommandInteractionDataOption{
			Name:  "reason",
			Type:  discordgo.ApplicationCommandOptionString,
			Value: *reason,
		})
	}

	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type:    discordgo.InteractionApplicationCommand,
			GuildID: "guild1",
			Member:  &discordgo.Member{User: &discordgo.User{ID: invokerID, Username: "mod"}},
			Data: discordgo.ApplicationCommandInteractionData{
				Name:    "ban",
				Options: opts,
				Resolved: &discordgo.ApplicationCommandInteractionDataResolved{
					Users: map[string]*discordgo.User{
						targetID: {ID: targetID, Username: "target"},
					},
				},
			},
		},
	}
}

func buildContextInteraction(invokerID, targetID, commandName string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type:    discordgo.InteractionApplicationCommand,
			GuildID: "guild1",
			Member:  &discordgo.Member{User: &discordgo.User{ID: invokerID, Username: "mod"}},
			Data: discordgo.ApplicationCommandInteractionData{
				Name:     commandName,
				TargetID: targetID,
				Resolved: &discordgo.ApplicationCommandInteractionDataResolved{
					Users: map[string]*discordgo.User{
						targetID: {ID: targetID, Username: "target"},
					},
				},
			},
		},
	}
}

func TestSlashBanCorrectUserAndParams(t *testing.T) {
	cap := &banCapture{}
	mod := newModule(t, cap)
	days := 3
	reason := "rule violation"
	s := mockSession()
	s.State.User = &discordgo.User{ID: "bot123"}

	mod.handleBanSlash(s, buildSlashInteraction("mod1", "target1", &days, &reason))

	require.Len(t, cap.banCalls, 1, "exactly one ban call")
	assert.Equal(t, "target1", cap.banCalls[0].userID)
	assert.Equal(t, "guild1", cap.banCalls[0].guildID)
	assert.Equal(t, "rule violation", cap.banCalls[0].reason)
	assert.Equal(t, 3, cap.banCalls[0].days)
}

func TestSlashBanDefaultDaysAndReason(t *testing.T) {
	cap := &banCapture{}
	mod := newModule(t, cap)
	s := mockSession()
	s.State.User = &discordgo.User{ID: "bot123"}

	mod.handleBanSlash(s, buildSlashInteraction("mod1", "target1", nil, nil))

	require.Len(t, cap.banCalls, 1)
	assert.Equal(t, "target1", cap.banCalls[0].userID)
	assert.Equal(t, 0, cap.banCalls[0].days)
	assert.Equal(t, "", cap.banCalls[0].reason)
}

func TestSlashBanOnlyBansOneUser(t *testing.T) {
	cap := &banCapture{}
	mod := newModule(t, cap)
	days := 1
	s := mockSession()
	s.State.User = &discordgo.User{ID: "bot123"}

	mod.handleBanSlash(s, buildSlashInteraction("mod1", "victim99", &days, nil))

	require.Len(t, cap.banCalls, 1, "must make exactly one ban call")
	assert.Equal(t, "victim99", cap.banCalls[0].userID, "must ban the requested user only")
}

func TestSlashBanRejectsSelfBan(t *testing.T) {
	cap := &banCapture{}
	mod := newModule(t, cap)
	s := mockSession()
	s.State.User = &discordgo.User{ID: "bot123"}

	mod.handleBanSlash(s, buildSlashInteraction("mod1", "mod1", nil, nil))

	assert.Empty(t, cap.banCalls, "no ban should be issued")
	require.Len(t, cap.edits, 1)
	assert.Contains(t, cap.edits[0], "cannot ban yourself")
}

func TestSlashBanRejectsBotBan(t *testing.T) {
	cap := &banCapture{}
	mod := newModule(t, cap)
	s := mockSession()
	s.State.User = &discordgo.User{ID: "bot123"}

	mod.handleBanSlash(s, buildSlashInteraction("mod1", "bot123", nil, nil))

	assert.Empty(t, cap.banCalls, "no ban should be issued")
	require.Len(t, cap.edits, 1)
	assert.Contains(t, cap.edits[0], "cannot ban myself")
}

func TestContextBanUserNoPurge(t *testing.T) {
	cap := &banCapture{}
	mod := newModule(t, cap)
	s := mockSession()
	s.State.User = &discordgo.User{ID: "bot123"}

	mod.handleBanContext(s, buildContextInteraction("mod1", "target2", "Ban User"))

	require.Len(t, cap.banCalls, 1, "exactly one ban call")
	assert.Equal(t, "target2", cap.banCalls[0].userID)
	assert.Equal(t, 0, cap.banCalls[0].days)
	assert.Equal(t, contextMenuReason, cap.banCalls[0].reason)
}

func TestContextBanPurge7Days(t *testing.T) {
	cap := &banCapture{}
	mod := newModule(t, cap)
	s := mockSession()
	s.State.User = &discordgo.User{ID: "bot123"}

	mod.handleBanContext(s, buildContextInteraction("mod1", "target3", "Ban + Purge Messages"))

	require.Len(t, cap.banCalls, 1, "exactly one ban call")
	assert.Equal(t, "target3", cap.banCalls[0].userID)
	assert.Equal(t, 7, cap.banCalls[0].days)
	assert.Equal(t, contextMenuReason, cap.banCalls[0].reason)
}

func TestContextBanRejectsSelfBan(t *testing.T) {
	cap := &banCapture{}
	mod := newModule(t, cap)
	s := mockSession()
	s.State.User = &discordgo.User{ID: "bot123"}

	mod.handleBanContext(s, buildContextInteraction("mod1", "mod1", "Ban User"))

	assert.Empty(t, cap.banCalls)
	require.Len(t, cap.edits, 1)
	assert.Contains(t, cap.edits[0], "cannot ban yourself")
}

func TestDMSentBeforeBan(t *testing.T) {
	cap := &banCapture{}
	mod := newModule(t, cap)
	s := mockSession()
	s.State.User = &discordgo.User{ID: "bot123"}

	var callOrder []string
	mod.opts.SendDM = func(_ *discordgo.Session, userID, message string) error {
		callOrder = append(callOrder, "dm")
		assert.Equal(t, "target1", userID)
		assert.Contains(t, message, "gamerpals.xyz")
		return nil
	}
	mod.opts.CreateBan = func(_ *discordgo.Session, _, userID, _ string, _ int) error {
		callOrder = append(callOrder, "ban")
		return nil
	}

	mod.handleBanSlash(s, buildSlashInteraction("mod1", "target1", nil, nil))

	require.Len(t, callOrder, 2)
	assert.Equal(t, "dm", callOrder[0], "DM must be sent before ban")
	assert.Equal(t, "ban", callOrder[1])
}

func TestDMFailureLogsBestPal(t *testing.T) {
	cap := &banCapture{}
	mod := newModule(t, cap)
	s := mockSession()
	s.State.User = &discordgo.User{ID: "bot123"}

	mod.opts.SendDM = func(_ *discordgo.Session, _ string, _ string) error {
		return assert.AnError
	}

	mod.handleBanSlash(s, buildSlashInteraction("mod1", "target1", nil, nil))

	require.Len(t, cap.banCalls, 1, "ban should still proceed despite DM failure")
	require.Len(t, cap.bestpalLog, 1, "DM failure should log to bestpal channel")
	assert.Contains(t, cap.bestpalLog[0], "Could not DM")
}

func TestModActionLogFields(t *testing.T) {
	cap := &banCapture{}
	mod := newModule(t, cap)
	days := 5
	reason := "spam"
	s := mockSession()
	s.State.User = &discordgo.User{ID: "bot123"}

	mod.handleBanSlash(s, buildSlashInteraction("mod1", "target1", &days, &reason))

	require.Len(t, cap.modLogs, 1)
	embed := cap.modLogs[0]
	assert.Equal(t, "🔨 User Banned", embed.Title)
	require.Len(t, embed.Fields, 5)
	assert.Contains(t, embed.Fields[0].Value, "target1")
	assert.Contains(t, embed.Fields[1].Value, "mod1")
	assert.Equal(t, "spam", embed.Fields[2].Value)
	assert.Contains(t, embed.Fields[3].Value, "5")
	assert.Equal(t, "slash command", embed.Fields[4].Value)
}

func TestSlashBanRejectsInvalidDays(t *testing.T) {
	cap := &banCapture{}
	mod := newModule(t, cap)
	days := 10
	s := mockSession()
	s.State.User = &discordgo.User{ID: "bot123"}

	mod.handleBanSlash(s, buildSlashInteraction("mod1", "target1", &days, nil))

	assert.Empty(t, cap.banCalls, "no ban should be issued for invalid days")
	require.Len(t, cap.edits, 1)
	assert.Contains(t, cap.edits[0], "between 0 and 7")
}

func TestSlashBanRejectsNilMember(t *testing.T) {
	cap := &banCapture{}
	mod := newModule(t, cap)
	s := mockSession()
	s.State.User = &discordgo.User{ID: "bot123"}

	inter := buildSlashInteraction("mod1", "target1", nil, nil)
	inter.Interaction.Member = nil

	mod.handleBanSlash(s, inter)

	assert.Empty(t, cap.banCalls, "no ban should be issued")
	require.Len(t, cap.edits, 1)
	assert.Contains(t, cap.edits[0], "only be used in a server")
}

func TestContextBanRejectsNilMember(t *testing.T) {
	cap := &banCapture{}
	mod := newModule(t, cap)
	s := mockSession()
	s.State.User = &discordgo.User{ID: "bot123"}

	inter := buildContextInteraction("mod1", "target1", "Ban User")
	inter.Interaction.Member = nil

	mod.handleBanContext(s, inter)

	assert.Empty(t, cap.banCalls, "no ban should be issued")
	require.Len(t, cap.edits, 1)
	assert.Contains(t, cap.edits[0], "only be used in a server")
}
