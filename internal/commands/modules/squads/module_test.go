package squads

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sentMsg struct {
	ChannelID string
	Msg       *discordgo.MessageSend
}

type fakeAPI struct {
	mu         sync.Mutex
	nextID     int
	responses  []*discordgo.InteractionResponse
	edits      []*discordgo.WebhookEdit
	sent       []sentMsg
	dms        []sentMsg
	pins       []string
	members    map[string]*discordgo.Member
	roles      map[string]string // role ID -> name
	channels   map[string]discordgo.GuildChannelCreateData
	roleAdds   []string // "user:role"
	roleRems   []string
	delCh      []string
	delRoles   []string
	permEdits  map[string][]*discordgo.PermissionOverwrite
	dmErr      error
	sendErr    map[string]error
	addErr     error
	removeErr  error
	deleteErr  error
	roleDelErr error
	memberErr  map[string]error
	catPerms   []*discordgo.PermissionOverwrite
	lastHuman  map[string]time.Time
	historyErr error
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{nextID: 100, members: map[string]*discordgo.Member{}, roles: map[string]string{},
		channels: map[string]discordgo.GuildChannelCreateData{}, permEdits: map[string][]*discordgo.PermissionOverwrite{}, sendErr: map[string]error{},
		memberErr: map[string]error{}, lastHuman: map[string]time.Time{}}
}

func (f *fakeAPI) id() string { f.nextID++; return fmt.Sprint(f.nextID) }

func (f *fakeAPI) Respond(_ *discordgo.Interaction, r *discordgo.InteractionResponse) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses = append(f.responses, r)
	return nil
}
func (f *fakeAPI) EditResponse(_ *discordgo.Interaction, e *discordgo.WebhookEdit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, e)
	return nil
}
func (f *fakeAPI) Send(ch string, msg *discordgo.MessageSend) (*discordgo.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.sendErr[ch]; err != nil {
		return nil, err
	}
	f.sent = append(f.sent, sentMsg{ch, msg})
	return &discordgo.Message{ID: f.id(), ChannelID: ch}, nil
}
func (f *fakeAPI) Pin(_, id string) error { f.pins = append(f.pins, id); return nil }
func (f *fakeAPI) Member(_, uid string) (*discordgo.Member, error) {
	if err := f.memberErr[uid]; err != nil {
		return nil, err
	}
	if m, ok := f.members[uid]; ok {
		return m, nil
	}
	return nil, &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownMember}}
}
func (f *fakeAPI) CreateRole(_ string, p *discordgo.RoleParams) (*discordgo.Role, error) {
	id := f.id()
	f.roles[id] = p.Name
	return &discordgo.Role{ID: id, Name: p.Name}, nil
}
func (f *fakeAPI) DeleteRole(_, id string) error {
	if f.roleDelErr != nil {
		return f.roleDelErr
	}
	f.delRoles = append(f.delRoles, id)
	return nil
}
func (f *fakeAPI) AddRole(_, uid, rid string) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.roleAdds = append(f.roleAdds, uid+":"+rid)
	return nil
}
func (f *fakeAPI) RemoveRole(_, uid, rid string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.roleRems = append(f.roleRems, uid+":"+rid)
	return nil
}
func (f *fakeAPI) CreateChannel(_ string, d discordgo.GuildChannelCreateData) (*discordgo.Channel, error) {
	id := f.id()
	f.channels[id] = d
	return &discordgo.Channel{ID: id, Name: d.Name}, nil
}
func (f *fakeAPI) EditChannelPerms(id string, o []*discordgo.PermissionOverwrite) error {
	f.permEdits[id] = o
	return nil
}
func (f *fakeAPI) DeleteChannel(id string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.delCh = append(f.delCh, id)
	return nil
}
func (f *fakeAPI) Channel(id string) (*discordgo.Channel, error) {
	return &discordgo.Channel{ID: id, Type: discordgo.ChannelTypeGuildCategory, PermissionOverwrites: f.catPerms}, nil
}
func (f *fakeAPI) LastHumanMessage(ch string, since time.Time) (time.Time, error) {
	if f.historyErr != nil {
		return time.Time{}, f.historyErr
	}
	if last := f.lastHuman[ch]; last.After(since) {
		return last, nil
	}
	return time.Time{}, nil
}
func (f *fakeAPI) DM(uid string, msg *discordgo.MessageSend) error {
	if f.dmErr != nil {
		return f.dmErr
	}
	f.dms = append(f.dms, sentMsg{uid, msg})
	return nil
}
func (f *fakeAPI) BotID() string { return "bot" }

func (f *fakeAPI) lastResponse() *discordgo.InteractionResponse {
	return f.responses[len(f.responses)-1]
}
func (f *fakeAPI) lastEdit() *discordgo.WebhookEdit { return f.edits[len(f.edits)-1] }

type fakeGames struct{ results []gameInfo }

func (g fakeGames) Search(string) ([]gameInfo, error) { return g.results, nil }
func (g fakeGames) Get(id int64) (gameInfo, error) {
	for _, r := range g.results {
		if r.ID == id {
			return r, nil
		}
	}
	return gameInfo{}, errors.New("not found")
}

const guild = "guild1"

func newTestModule(t *testing.T, kv map[string]any) (*Module, *fakeAPI, *database.DB, *time.Time) {
	t.Helper()
	db, err := database.NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	base := map[string]any{
		"gamerpals_server_id":                 guild,
		config.KeySquadsEnabled:               true,
		config.KeySquadsIntroButton:           true,
		config.KeySquadsCategoryID:            "cat",
		config.KeySquadsModRoleID:             "mods",
		config.KeySquadsRegionEURoleID:        "role-eu",
		config.KeySquadsRegionNARoleID:        "role-na",
		config.KeySquadsAge1RoleID:            "role-18",
		config.KeySquadsAge2RoleID:            "role-25",
		config.KeyIntroductionsForumChannelID: "intros",
		config.KeyModActionLogChannelID:       "modlog",
	}
	for k, v := range kv {
		base[k] = v
	}
	now := t0
	api := newFakeAPI()
	m := &Module{
		config: config.NewMockConfig(base), db: db, api: api,
		games: fakeGames{results: []gameInfo{
			{ID: 1, Name: "Deep Rock Galactic", Year: 2018, Tags: []string{"Shooter"}},
			{ID: 2, Name: "Valheim", Year: 2021, Tags: []string{"Survival"}},
		}},
		now: func() time.Time { return now }, randN: func(int) int { return 0 },
		locks: map[string]*sync.Mutex{}, channels: map[string]int64{}, touched: map[int64]time.Time{},
	}
	m.service = &Service{m: m}
	return m, api, db, &now
}

func member(api *fakeAPI, uid string, roles ...string) {
	api.members[uid] = &discordgo.Member{User: &discordgo.User{ID: uid}, Roles: roles}
}

func component(uid, customID string, values ...string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent, GuildID: guild, ChannelID: "thread1",
		Member: &discordgo.Member{User: &discordgo.User{ID: uid}},
		Data:   discordgo.MessageComponentInteractionData{CustomID: customID, Values: values},
	}}
}

func modalSubmit(uid, customID, value string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit, GuildID: guild,
		Member: &discordgo.Member{User: &discordgo.User{ID: uid}},
		Data: discordgo.ModalSubmitInteractionData{CustomID: customID, Components: []discordgo.MessageComponent{
			&discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.TextInput{CustomID: "q", Value: value}}},
		}},
	}}
}

func command(uid string, perms int64, name, sub string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand, GuildID: guild,
		Member: &discordgo.Member{User: &discordgo.User{ID: uid}, Permissions: perms},
		Data: discordgo.ApplicationCommandInteractionData{Name: name, Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: sub, Type: discordgo.ApplicationCommandOptionSubCommand, Options: opts},
		}},
	}}
}

func editContent(e *discordgo.WebhookEdit) string {
	if e.Content == nil {
		return ""
	}
	return *e.Content
}

func TestPickerFlowQueuesPerson(t *testing.T) {
	m, api, db, _ := newTestModule(t, nil)
	join := prefix + "join:" + guild + ":alice:thread1"

	m.HandleComponent(nil, component("bob", join))
	assert.Contains(t, api.lastResponse().Data.Content, "for the person who posted this intro")

	m.HandleComponent(nil, component("alice", join))
	view := api.lastResponse().Data
	assert.Contains(t, view.Content, "Your squad games (0/25)")
	buttons := view.Components[0].(discordgo.ActionsRow).Components
	assert.True(t, buttons[1].(discordgo.Button).Disabled, "Finish is disabled until a game is picked")

	ctx := pickerCtx{GuildID: guild, ThreadID: "thread1"}
	m.HandleComponent(nil, component("alice", ctx.id("add")))
	assert.Equal(t, discordgo.InteractionResponseModal, api.lastResponse().Type)

	m.HandleModalSubmit(nil, modalSubmit("alice", ctx.id("search"), "deep rock"))
	progress := api.lastResponse()
	assert.Equal(t, discordgo.InteractionResponseUpdateMessage, progress.Type, "search shows progress right away")
	assert.Contains(t, progress.Data.Content, "Searching for **deep rock**")
	assert.Empty(t, progress.Data.Components, "picker buttons are hidden while searching")
	res := (*api.lastEdit().Components)[0].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
	require.Len(t, res.Options, 3)
	assert.Equal(t, "Deep Rock Galactic (2018)", res.Options[0].Label)
	assert.Equal(t, "again", res.Options[2].Value)

	m.HandleComponent(nil, component("alice", ctx.id("res"), "again"))
	assert.Equal(t, discordgo.InteractionResponseModal, api.lastResponse().Type, "search again reopens the form")

	m.HandleComponent(nil, component("alice", ctx.id("res"), "1"))
	assert.Contains(t, editContent(api.lastEdit()), "Added **Deep Rock Galactic**")
	m.HandleComponent(nil, component("alice", ctx.id("res"), "1"))
	assert.Contains(t, editContent(api.lastEdit()), "already picked")
	m.HandleComponent(nil, component("alice", ctx.id("res"), "2"))
	m.HandleComponent(nil, component("alice", ctx.id("rm"), "2"))
	assert.Contains(t, api.lastResponse().Data.Content, "1. Deep Rock Galactic (2018)")
	assert.NotContains(t, api.lastResponse().Data.Content, "Valheim")

	m.HandleComponent(nil, component("alice", ctx.id("done")))
	assert.Contains(t, editContent(api.lastEdit()), "You're in the squad queue")
	e, ok, err := db.SquadQueueEntryFor(guild, "alice")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "thread1", e.IntroThreadID)
	picks, _ := db.SquadPicks(guild, "alice")
	require.Len(t, picks, 1)
	assert.Equal(t, []string{"Shooter"}, picks[0].Tags)
}

func TestPickerClosedWhenDisabled(t *testing.T) {
	m, api, _, _ := newTestModule(t, map[string]any{config.KeySquadsEnabled: false})
	m.HandleComponent(nil, component("alice", prefix+"join:"+guild+":alice:t"))
	assert.Contains(t, api.lastResponse().Data.Content, "aren't open")
}

// queue puts people in the queue with the given picks and roles.
func queue(t *testing.T, m *Module, api *fakeAPI, db *database.DB, at time.Time, uids []string, games []int64, roles ...string) {
	t.Helper()
	for _, uid := range uids {
		member(api, uid, roles...)
		for _, g := range games {
			_, _, err := db.AddSquadPick(guild, uid, database.SquadPick{GameID: g, Name: fmt.Sprintf("Game %d", g), Tags: []string{"Shooter"}}, maxPicks, at)
			require.NoError(t, err)
		}
		require.NoError(t, db.EnqueueSquadUser(guild, uid, "intro-"+uid, at))
	}
}

func TestRunMatchCreatesSquad(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	people := []string{"u1", "u2", "u3", "u4", "u5", "u6"}
	queue(t, m, api, db, now.Add(-time.Hour), people, []int64{1}, "role-eu", "role-25")
	queue(t, m, api, db, now.Add(-time.Hour), []string{"na1"}, []int64{1}, "role-na", "role-25")

	require.NoError(t, m.tick())

	squads, err := db.ListSquads(guild)
	require.NoError(t, err)
	require.Len(t, squads, 1)
	sq := squads[0]
	assert.Equal(t, "Squad Axolotl", sq.Name)
	assert.Equal(t, 6, sq.Members)

	ch := api.channels[sq.ChannelID]
	assert.Equal(t, "squad-axolotl", ch.Name)
	assert.Equal(t, "cat", ch.ParentID)
	everyone := ch.PermissionOverwrites[0]
	assert.Equal(t, guild, everyone.ID)
	assert.NotZero(t, everyone.Deny&discordgo.PermissionSendMessages, "non-members can't chat")
	assert.Zero(t, everyone.Deny&discordgo.PermissionViewChannel, "public squads are readable")
	assert.Equal(t, "mods", ch.PermissionOverwrites[2].ID)
	assert.NotZero(t, ch.PermissionOverwrites[2].Allow&discordgo.PermissionViewChannel)

	for _, uid := range people {
		assert.Contains(t, api.roleAdds, uid+":"+sq.RoleID)
	}
	welcome := api.sent[0]
	assert.Equal(t, sq.ChannelID, welcome.ChannelID)
	assert.Contains(t, welcome.Msg.Content, "Welcome to Squad Axolotl")
	assert.Contains(t, welcome.Msg.Content, "6 of you play **Game 1**")
	assert.Contains(t, welcome.Msg.Content, "[intro](https://discord.com/channels/"+guild+"/intro-u1)")
	assert.ElementsMatch(t, people, welcome.Msg.AllowedMentions.Users)
	assert.Len(t, api.pins, 1)

	q, _ := db.SquadQueue(guild)
	assert.Equal(t, []string{"na1"}, []string{q[0].UserID}, "only the NA person is still waiting")

	// The interval hasn't passed, so a second tick doesn't match again.
	api.channels = map[string]discordgo.GuildChannelCreateData{}
	require.NoError(t, m.tick())
	assert.Empty(t, api.channels)
}

func TestPrivateVisibilityHidesChannel(t *testing.T) {
	o := overwrites(guild, "squadrole", "mods", "bot", config.SquadVisibilityPrivate, nil)
	assert.NotZero(t, o[0].Deny&discordgo.PermissionViewChannel)
	assert.NotZero(t, o[1].Allow&discordgo.PermissionSendMessages)
	assert.Equal(t, "bot", o[3].ID)
	assert.Equal(t, discordgo.PermissionOverwriteTypeMember, o[3].Type)
}

func TestLateJoinerFillsYoungSquad(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	queue(t, m, api, db, now.Add(-time.Hour), []string{"u1", "u2", "u3", "u4", "u5", "u6"}, []int64{1}, "role-eu", "role-25")
	require.NoError(t, m.tick())

	queue(t, m, api, db, *now, []string{"late"}, []int64{1}, "role-eu", "role-25")
	*now = now.Add(49 * time.Hour)
	require.NoError(t, m.tick())

	sq, ok, err := db.SquadForUser(guild, "late")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 7, sq.Members)
	last := api.sent[len(api.sent)-1]
	assert.Equal(t, sq.ChannelID, last.ChannelID)
	assert.Contains(t, last.Msg.Content, "<@late> joined the squad! They play **Game 1**.")
	assert.Contains(t, last.Msg.Content, "📝 Intro: [intro](https://discord.com/channels/"+guild+"/intro-late)")
}

func TestUnmatchedPersonIsDMedOrPingedInIntro(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	queue(t, m, api, db, now.Add(-8*24*time.Hour), []string{"lonely"}, []int64{1}, "role-eu")
	queue(t, m, api, db, now.Add(-8*24*time.Hour), []string{"closed-dms"}, []int64{2}, "role-na")
	api.dmErr = nil
	require.NoError(t, m.tick())
	require.Len(t, api.dms, 2)
	assert.Contains(t, api.dms[0].Msg.Content, "couldn't find you a squad")
	btn := api.dms[0].Msg.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	assert.True(t, strings.HasPrefix(btn.CustomID, prefix+"join:"+guild+":"))
	q, _ := db.SquadQueue(guild)
	assert.Empty(t, q)

	// With DMs closed, the message goes to their intro thread instead.
	m2, api2, db2, now2 := newTestModule(t, nil)
	queue(t, m2, api2, db2, now2.Add(-8*24*time.Hour), []string{"closed"}, []int64{1}, "role-eu")
	api2.dmErr = errors.New("cannot send messages to this user")
	require.NoError(t, m2.tick())
	require.Len(t, api2.sent, 1)
	assert.Equal(t, "intro-closed", api2.sent[0].ChannelID)
	assert.Contains(t, api2.sent[0].Msg.Content, "<@closed>")
}

func TestPersonWhoLeftIsDroppedFromQueue(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	queue(t, m, api, db, now.Add(-time.Hour), []string{"gone"}, []int64{1})
	delete(api.members, "gone")
	require.NoError(t, m.tick())
	q, _ := db.SquadQueue(guild)
	assert.Empty(t, q)
}

func makeSquad(t *testing.T, m *Module, api *fakeAPI, db *database.DB, uids ...string) database.Squad {
	t.Helper()
	var group []*candidate
	for _, uid := range uids {
		member(api, uid, "role-eu")
		group = append(group, &candidate{UserID: uid, Games: map[int64]string{1: "Game 1"}, Tags: map[string]bool{}})
	}
	sq, err := m.createSquad(guild, group, false)
	require.NoError(t, err)
	sq, _, err = db.GetSquad(sq.ID)
	require.NoError(t, err)
	return sq
}

func TestQuietSquadIsWarnedThenDeleted(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	sq := makeSquad(t, m, api, db, "a", "b", "c")

	*now = now.Add(7*24*time.Hour - time.Minute)
	require.NoError(t, m.tick())
	sq, _, _ = db.GetSquad(sq.ID)
	assert.True(t, sq.WarnedAt.IsZero())

	*now = now.Add(2 * time.Minute)
	require.NoError(t, m.tick())
	warn := api.sent[len(api.sent)-1]
	assert.Contains(t, warn.Msg.Content, "will be deleted")
	assert.Equal(t, []string{sq.RoleID}, warn.Msg.AllowedMentions.Roles)

	// Chat cancels the warning.
	*now = now.Add(time.Hour)
	m.OnMessageCreate(nil, &discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: sq.ChannelID, Author: &discordgo.User{ID: "a"}}})
	sq, _, _ = db.GetSquad(sq.ID)
	assert.True(t, sq.WarnedAt.IsZero())
	assert.Equal(t, now.Unix(), sq.LastActivityAt.Unix())

	// Bot messages don't count.
	*now = now.Add(7*24*time.Hour + time.Minute)
	require.NoError(t, m.tick())
	m.OnMessageCreate(nil, &discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: sq.ChannelID, Author: &discordgo.User{ID: "x", Bot: true}}})
	*now = now.Add(3 * 24 * time.Hour)
	require.NoError(t, m.tick())
	_, ok, _ := db.GetSquad(sq.ID)
	assert.False(t, ok)
	assert.Contains(t, api.delCh, sq.ChannelID)
	assert.Contains(t, api.delRoles, sq.RoleID)
	assert.Equal(t, "modlog", api.sent[len(api.sent)-1].ChannelID)
	log := modLogEntry(t, api)
	assert.Equal(t, "🗑️ Squad Deleted", log.Title)
	assert.Contains(t, embedText(log), "It was quiet for too long")
}

func TestLeavingBelowTwoDeletesSquad(t *testing.T) {
	m, api, db, _ := newTestModule(t, nil)
	sq := makeSquad(t, m, api, db, "a", "b", "c")

	m.HandleComponent(nil, component("zed", fmt.Sprintf("%sleave:%d", prefix, sq.ID)))
	assert.Contains(t, api.lastResponse().Data.Content, "not in this squad")

	m.HandleComponent(nil, component("a", fmt.Sprintf("%sleave:%d", prefix, sq.ID)))
	assert.Contains(t, api.lastResponse().Data.Content, "Leave **Squad Axolotl**?")
	m.HandleComponent(nil, component("a", fmt.Sprintf("%sleaveyes:%d", prefix, sq.ID)))
	assert.Contains(t, editContent(api.lastEdit()), "You left **Squad Axolotl**")
	assert.Contains(t, api.roleRems, "a:"+sq.RoleID)
	assert.Contains(t, api.sent[len(api.sent)-1].Msg.Content, "<@a> left the squad")

	m.HandleComponent(nil, component("b", fmt.Sprintf("%sleaveyes:%d", prefix, sq.ID)))
	_, ok, _ := db.GetSquad(sq.ID)
	assert.False(t, ok, "one member left, so the squad is deleted")
	assert.Contains(t, api.delCh, sq.ChannelID)
	_, inSquad, _ := db.SquadForUser(guild, "c")
	assert.False(t, inSquad)
}

func TestMemberLeavingServerLeavesSquadAndQueue(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	queue(t, m, api, db, *now, []string{"q"}, []int64{1})
	for _, uid := range []string{"a", "q"} {
		m.OnGuildMemberRemove(nil, &discordgo.GuildMemberRemove{Member: &discordgo.Member{GuildID: guild, User: &discordgo.User{ID: uid}}})
	}
	sq, _, _ = db.GetSquad(sq.ID)
	assert.Equal(t, 2, sq.Members)
	q, _ := db.SquadQueue(guild)
	assert.Empty(t, q)
}

func TestChannelDeletedByHandCleansUp(t *testing.T) {
	m, api, db, _ := newTestModule(t, nil)
	sq := makeSquad(t, m, api, db, "a", "b")
	m.OnChannelDelete(nil, &discordgo.ChannelDelete{Channel: &discordgo.Channel{ID: sq.ChannelID, GuildID: guild}})
	_, ok, _ := db.GetSquad(sq.ID)
	assert.False(t, ok)
	assert.Contains(t, api.delRoles, sq.RoleID)
}

func TestAdminCommands(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	sq := makeSquad(t, m, api, db, "a", "b")
	member(api, "c", "role-eu")
	userOpt := func(uid string) *discordgo.ApplicationCommandInteractionDataOption {
		return &discordgo.ApplicationCommandInteractionDataOption{Name: "user", Type: discordgo.ApplicationCommandOptionUser, Value: uid}
	}
	squadOpt := &discordgo.ApplicationCommandInteractionDataOption{Name: "squad", Type: discordgo.ApplicationCommandOptionString, Value: fmt.Sprint(sq.ID)}

	m.handleAdmin(nil, command("rando", 0, "squad-admin", "status"))
	assert.Contains(t, api.lastResponse().Data.Content, "Ban Members")

	m.handleAdmin(nil, command("mod", discordgo.PermissionBanMembers, "squad-admin", "add", userOpt("c"), squadOpt))
	assert.Contains(t, editContent(api.lastEdit()), "Added <@c> to **Squad Axolotl**")
	got, _, _ := db.GetSquad(sq.ID)
	assert.Equal(t, 3, got.Members)

	m.handleAdmin(nil, command("mod", discordgo.PermissionBanMembers, "squad-admin", "add", userOpt("c"), squadOpt))
	assert.Contains(t, editContent(api.lastEdit()), "already in **Squad Axolotl**")

	m.handleAdmin(nil, command("mod", discordgo.PermissionBanMembers, "squad-admin", "remove", userOpt("c")))
	assert.Contains(t, editContent(api.lastEdit()), "Removed <@c>")
	log := modLogEntry(t, api)
	assert.Equal(t, "➖ Removed from Squad", log.Title)
	assert.Contains(t, embedText(log), "Member: <@c>")
	assert.Contains(t, embedText(log), "Squad: **Squad Axolotl**")
	assert.Contains(t, embedText(log), "By: <@mod>")

	queue(t, m, api, db, now.Add(-time.Hour), []string{"q1", "q2"}, []int64{1}, "role-eu")
	m.handleAdmin(nil, command("mod", discordgo.PermissionBanMembers, "squad-admin", "status"))
	assert.Equal(t, discordgo.InteractionResponseDeferredChannelMessageWithSource, api.lastResponse().Type)
	status := editContent(api.lastEdit())
	assert.Contains(t, status, "**Queue:** 2 (Europe 2)")
	assert.Contains(t, status, "Squad Axolotl")

	m.handleAdmin(nil, command("mod", discordgo.PermissionBanMembers, "squad-admin", "user", userOpt("q1")))
	assert.Contains(t, editContent(api.lastEdit()), "**Region:** Europe")
	assert.Contains(t, editContent(api.lastEdit()), "Game 1")

	m.handleAdmin(nil, command("mod", discordgo.PermissionBanMembers, "squad-admin", "dryrun"))
	dry := editContent(api.lastEdit())
	assert.Contains(t, dry, "Dry run")
	assert.Contains(t, dry, "Still waiting:** <@q1> <@q2>")
	q, _ := db.SquadQueue(guild)
	assert.Len(t, q, 2, "a dry run changes nothing")

	m.handleAdmin(nil, command("mod", discordgo.PermissionBanMembers, "squad-admin", "queue-remove", userOpt("q1")))
	q, _ = db.SquadQueue(guild)
	assert.Len(t, q, 1)

	m.handleAdmin(nil, command("mod", discordgo.PermissionBanMembers, "squad-admin", "refresh-permissions"))
	assert.Contains(t, api.permEdits, sq.ChannelID)

	m.handleAdmin(nil, command("mod", discordgo.PermissionBanMembers, "squad-admin", "delete", squadOpt))
	confirm := api.lastResponse().Data.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	_, ok, _ := db.GetSquad(sq.ID)
	assert.True(t, ok, "delete asks first")
	m.HandleComponent(nil, component("rando", confirm.CustomID))
	_, ok, _ = db.GetSquad(sq.ID)
	assert.True(t, ok, "only mods can confirm")
	modClick := component("mod", confirm.CustomID)
	modClick.Member.Permissions = discordgo.PermissionBanMembers
	m.HandleComponent(nil, modClick)
	_, ok, _ = db.GetSquad(sq.ID)
	assert.False(t, ok)
}

func TestIntroThreadGetsJoinButton(t *testing.T) {
	m, api, _, _ := newTestModule(t, nil)
	introRetryDelay = 0
	m.postIntroPrompt(nil, &discordgo.Channel{ID: "t1", GuildID: guild, ParentID: "intros", OwnerID: "alice"}, 3)
	require.Len(t, api.sent, 1)
	btn := api.sent[0].Msg.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	assert.Equal(t, prefix+"join:"+guild+":alice:t1", btn.CustomID)

	api.sendErr["t2"] = errors.New("nope")
	m.postIntroPrompt(nil, &discordgo.Channel{ID: "t2", GuildID: guild, ParentID: "intros", OwnerID: "bob"}, 2)
	assert.Len(t, api.sent, 1)
}

func TestIntroButtonToggle(t *testing.T) {
	intro := &discordgo.Channel{ID: "t1", GuildID: guild, ParentID: "intros", OwnerID: "alice"}
	m, _, _, _ := newTestModule(t, nil)
	assert.True(t, m.wantsIntroButton(intro))
	assert.False(t, m.wantsIntroButton(&discordgo.Channel{ID: "t2", GuildID: guild, ParentID: "other", OwnerID: "alice"}))

	m, _, _, _ = newTestModule(t, map[string]any{config.KeySquadsIntroButton: false})
	assert.False(t, m.wantsIntroButton(intro), "intro posting can be off while squads are enabled")
	assert.Contains(t, m.statusText(guild), "Post on intros is off")

	m, _, _, _ = newTestModule(t, map[string]any{config.KeySquadsEnabled: false})
	assert.False(t, m.wantsIntroButton(intro))
}

func TestIntroLink(t *testing.T) {
	assert.Empty(t, introLink(guild, &candidate{UserID: "a", Name: "Alice"}))
	assert.Equal(t, "[Alice](https://discord.com/channels/"+guild+"/t1)",
		introLink(guild, &candidate{UserID: "a", Name: "Alice", IntroID: "t1"}))
	assert.Equal(t, `[\[x\]\_y\_ z](https://discord.com/channels/`+guild+`/t2)`,
		introLink(guild, &candidate{Name: "[x]_y_\nz", IntroID: "t2"}))

	text, extra := welcomeText(guild, "Squad Owl", "r", "", []*candidate{
		{UserID: "a", Name: "Alice", IntroID: "t1"}, {UserID: "b", Name: "Bob"}, {UserID: "c", Name: "Cy", IntroID: "t3"},
	}, time.Hour)
	assert.Contains(t, text, "📝 Intros: [Alice](https://discord.com/channels/"+guild+"/t1) · [Cy](https://discord.com/channels/"+guild+"/t3)")
	assert.Empty(t, extra)
	text, extra = welcomeText(guild, "Squad Owl", "r", "", []*candidate{{UserID: "b"}}, time.Hour)
	assert.NotContains(t, text, "Intros")
	assert.Empty(t, extra)
}

func TestWelcomeFitsWithFullSquadOfIntros(t *testing.T) {
	var group []*candidate
	for n := 0; n < 25; n++ {
		group = append(group, &candidate{
			UserID:  fmt.Sprintf("1234567890123456%02d", n),
			Name:    strings.Repeat("ñ", 32),
			IntroID: fmt.Sprintf("9876543210987654%02d", n),
		})
	}
	text, extra := welcomeText("123456789012345678", "Squad Owl", "123456789012345678", "", group, time.Hour)
	assert.LessOrEqual(t, utf8.RuneCountInString(text), 2000)
	for n := range group {
		assert.Contains(t, text, mention(group[n].UserID))
	}
	assert.NotContains(t, text, "Intros")
	assert.Greater(t, len(extra), 1)
	links := 0
	for _, post := range extra {
		assert.LessOrEqual(t, utf8.RuneCountInString(post), 2000)
		assert.True(t, strings.HasPrefix(post, "📝 Intros: "))
		links += strings.Count(post, "](https://discord.com/channels/")
	}
	assert.Equal(t, 25, links)
}

func TestOverwritesFollowCategory(t *testing.T) {
	cat := []*discordgo.PermissionOverwrite{
		{ID: guild, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel},
		{ID: "verified", Type: discordgo.PermissionOverwriteTypeRole, Allow: discordgo.PermissionViewChannel | discordgo.PermissionSendMessages},
		{ID: "muted", Type: discordgo.PermissionOverwriteTypeMember, Deny: discordgo.PermissionSendMessages},
	}
	public := overwrites(guild, "squadrole", "mods", "bot", config.SquadVisibilityPublicReadOnly, cat)
	assert.NotZero(t, public[0].Deny&discordgo.PermissionViewChannel, "unverified people still can't see it")
	assert.NotZero(t, public[0].Deny&discordgo.PermissionSendMessages)
	verified := public[2]
	assert.Equal(t, "verified", verified.ID)
	assert.NotZero(t, verified.Allow&discordgo.PermissionViewChannel, "verified people can read")
	assert.Zero(t, verified.Allow&discordgo.PermissionSendMessages, "but not post")
	assert.NotZero(t, public[3].Deny&discordgo.PermissionSendMessages, "member denies are kept")
	assert.NotZero(t, cat[1].Allow&discordgo.PermissionSendMessages, "the category's overwrites aren't modified")

	private := overwrites(guild, "squadrole", "mods", "bot", config.SquadVisibilityPrivate, cat)
	assert.Zero(t, private[2].Allow&discordgo.PermissionViewChannel)
	assert.NotZero(t, private[1].Allow&discordgo.PermissionViewChannel, "members see it")
}

func TestCreateSquadCopiesCategoryPermissions(t *testing.T) {
	m, api, db, _ := newTestModule(t, nil)
	api.catPerms = []*discordgo.PermissionOverwrite{{ID: "verified", Type: discordgo.PermissionOverwriteTypeRole, Allow: discordgo.PermissionViewChannel}}
	sq := makeSquad(t, m, api, db, "a", "b")
	perms := api.channels[sq.ChannelID].PermissionOverwrites
	assert.Equal(t, sq.RoleID, perms[1].ID)
	assert.Equal(t, "verified", perms[2].ID)
}

func TestFailedRoleChangesAreRetried(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	api.addErr = errors.New("discord hiccup")
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	ops, _ := db.SquadRoleOps(guild)
	assert.Len(t, ops, 3)

	api.addErr = nil
	*now = now.Add(5 * time.Minute)
	require.NoError(t, m.tick())
	ops, _ = db.SquadRoleOps(guild)
	assert.Empty(t, ops)
	assert.Contains(t, api.roleAdds, "a:"+sq.RoleID)

	api.removeErr = errors.New("discord hiccup")
	m.HandleComponent(nil, component("a", fmt.Sprintf("%sleaveyes:%d", prefix, sq.ID)))
	ops, _ = db.SquadRoleOps(guild)
	require.Len(t, ops, 1)
	assert.False(t, ops[0].Add)
	api.removeErr = nil
	require.NoError(t, m.tick())
	assert.Contains(t, api.roleRems, "a:"+sq.RoleID)
	ops, _ = db.SquadRoleOps(guild)
	assert.Empty(t, ops)
}

func TestFailedDeleteKeepsSquadForRetry(t *testing.T) {
	m, api, db, _ := newTestModule(t, nil)
	sq := makeSquad(t, m, api, db, "a", "b")
	api.deleteErr = errors.New("discord hiccup")
	m.HandleComponent(nil, component("a", fmt.Sprintf("%sleaveyes:%d", prefix, sq.ID)))
	_, ok, _ := db.GetSquad(sq.ID)
	assert.True(t, ok, "kept while the channel still exists")

	api.deleteErr = nil
	require.NoError(t, m.tick())
	_, ok, _ = db.GetSquad(sq.ID)
	assert.False(t, ok)
	assert.Contains(t, api.delCh, sq.ChannelID)
}

func TestPartlyDeletedSquadTakesNobody(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	api.roleDelErr = errors.New("discord hiccup")
	require.Error(t, m.deleteSquad(sq, "a mod deleted it"))
	sq, ok, _ := db.GetSquad(sq.ID)
	require.True(t, ok)
	assert.True(t, sq.Deleting())
	assert.Contains(t, m.statusText(guild), "deleting (retrying")
	assert.Contains(t, m.adminAdd(component("mod", "x"), "x", sq.ID), "being deleted")

	// The retry still fails, and matching must not refill the squad.
	queue(t, m, api, db, now.Add(-49*time.Hour), []string{"x"}, []int64{1}, "role-eu")
	require.NoError(t, m.tick())
	got, _, _ := db.GetSquad(sq.ID)
	assert.Equal(t, 3, got.Members)
	_, in, _ := db.SquadForUser(guild, "x")
	assert.False(t, in)

	api.roleDelErr = nil
	*now = now.Add(5 * time.Minute)
	require.NoError(t, m.tick())
	_, ok, _ = db.GetSquad(sq.ID)
	assert.False(t, ok, "the next check finishes the delete")
	assert.Contains(t, embedText(modLogEntry(t, api)), "Reason: A mod deleted it")
}

func TestDeparturesReconciledInFullSquads(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	full := makeSquad(t, m, api, db, "a", "b", "c", "d", "e", "f", "g", "h")
	pair := makeSquad(t, m, api, db, "p", "q")
	*now = now.Add(30 * 24 * time.Hour) // long past the young window
	api.lastHuman[full.ChannelID] = *now
	api.lastHuman[pair.ChannelID] = *now
	delete(api.members, "a")
	delete(api.members, "q")
	api.memberErr["b"] = errors.New("discord hiccup")
	require.NoError(t, m.tick())

	got, _, _ := db.GetSquad(full.ID)
	assert.Equal(t, 7, got.Members, "only the confirmed departure is removed")
	_, in, _ := db.SquadForUser(guild, "b")
	assert.True(t, in, "a failed lookup keeps the membership")
	_, ok, _ := db.GetSquad(pair.ID)
	assert.False(t, ok, "a squad left with one member is deleted")

	// Checked at most hourly.
	delete(api.members, "c")
	*now = now.Add(10 * time.Minute)
	require.NoError(t, m.tick())
	got, _, _ = db.GetSquad(full.ID)
	assert.Equal(t, 7, got.Members)
	*now = now.Add(time.Hour)
	require.NoError(t, m.tick())
	got, _, _ = db.GetSquad(full.ID)
	assert.Equal(t, 6, got.Members)
}

func TestOpenSquadSkippedWhenAMemberCantBeLoaded(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	sq := makeSquad(t, m, api, db, "a", "b", "c", "d", "e", "f", "g")
	api.memberErr["a"] = errors.New("discord hiccup")
	queue(t, m, api, db, now.Add(-49*time.Hour), []string{"x", "y"}, []int64{1}, "role-eu")
	require.NoError(t, m.tick())
	got, _, _ := db.GetSquad(sq.ID)
	assert.Equal(t, 7, got.Members, "nobody joins a squad whose roster couldn't be checked")
}

func TestChatMissedWhileOfflineSavesSquad(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	sq := makeSquad(t, m, api, db, "a", "b")
	*now = now.Add(7*24*time.Hour + time.Minute)
	require.NoError(t, m.tick())
	sq, _, _ = db.GetSquad(sq.ID)
	require.False(t, sq.WarnedAt.IsZero())

	// The bot was offline while people chatted.
	api.lastHuman[sq.ChannelID] = now.Add(time.Hour)
	*now = now.Add(3*24*time.Hour + time.Minute)
	require.NoError(t, m.tick())
	sq, ok, _ := db.GetSquad(sq.ID)
	require.True(t, ok)
	assert.True(t, sq.WarnedAt.IsZero())

	// A failed history check never deletes.
	api.historyErr = errors.New("discord hiccup")
	*now = now.Add(30 * 24 * time.Hour)
	require.NoError(t, m.tick())
	_, ok, _ = db.GetSquad(sq.ID)
	assert.True(t, ok)
}

func TestPickerFitsDiscordLimit(t *testing.T) {
	m, _, db, now := newTestModule(t, nil)
	for n := range maxPicks {
		_, _, err := db.AddSquadPick(guild, "u", database.SquadPick{GameID: int64(n + 1), Name: strings.Repeat("Very Long Game Title ", 10), Year: 2020}, maxPicks, *now)
		require.NoError(t, err)
	}
	require.NoError(t, db.EnqueueSquadUser(guild, "u", "", *now))
	view := m.pickerView(pickerCtx{GuildID: guild}, "u", "✅ Added **"+strings.Repeat("x", 300)+"**.")
	assert.LessOrEqual(t, len([]rune(view.Content)), 2000)
	assert.Contains(t, view.Content, "You're in the queue", "the footer isn't cut off")
}

func TestUnmatchedFallsBackToLookedUpIntro(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	m.introThread = func(_, uid string) string { return "found-" + uid }
	member(api, "z", "role-eu")
	_, _, _ = db.AddSquadPick(guild, "z", database.SquadPick{GameID: 1, Name: "G"}, maxPicks, *now)
	require.NoError(t, db.EnqueueSquadUser(guild, "z", "", now.Add(-8*24*time.Hour)))
	api.dmErr = errors.New("closed")
	require.NoError(t, m.tick())
	require.Len(t, api.sent, 1)
	assert.Equal(t, "found-z", api.sent[0].ChannelID)
}

func TestSquadLeaveCommandDequeues(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	queue(t, m, api, db, *now, []string{"q"}, []int64{1})
	m.handleSquad(nil, command("q", 0, "squad", "leave"))
	assert.Contains(t, editContent(api.lastEdit()), "You've left the squad queue")
	_, ok, _ := db.SquadQueueEntryFor(guild, "q")
	assert.False(t, ok)
}

func TestIGDBString(t *testing.T) {
	assert.Equal(t, `"Halo"`, igdbString("Halo"))
	assert.Equal(t, `"say \"hi\" \\ bye"`, igdbString(`say "hi" \ bye`))
}

func TestRankGames(t *testing.T) {
	games := []gameInfo{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}}
	tier := map[int64]int{1: 0, 2: 1, 3: 1, 4: 2, 5: 1}
	pop := map[int64]float64{1: 0.001, 2: 0.01, 3: 0.5, 4: 0.9}
	rankGames(games, tier, pop)
	var ids []int64
	for _, g := range games {
		ids = append(ids, g.ID)
	}
	assert.Equal(t, []int64{1, 3, 2, 5, 4}, ids)

	games = []gameInfo{{ID: 9}, {ID: 8}}
	rankGames(games, map[int64]int{}, nil)
	assert.Equal(t, int64(9), games[0].ID, "no scores keeps the original order")
}

func TestPopularityQuery(t *testing.T) {
	assert.Equal(t, "fields game_id,value; where game_id = (7,42) & popularity_type = 1; limit 2;",
		popularityQuery([]gameInfo{{ID: 7}, {ID: 42}}))
}

func TestRetryRateLimited(t *testing.T) {
	orig := rateLimitPause
	rateLimitPause = time.Millisecond
	defer func() { rateLimitPause = orig }()
	direct := func(fn func() error) error { return fn() }

	calls := 0
	err := retryRateLimited(direct, func() error {
		calls++
		if calls == 1 {
			return igdb.ErrManyRequests
		}
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 2, calls, "a 429 is retried once")

	calls = 0
	err = retryRateLimited(direct, func() error { calls++; return igdb.ErrManyRequests })
	assert.ErrorIs(t, err, igdb.ErrManyRequests)
	assert.Equal(t, 2, calls, "gives up after one retry")

	calls = 0
	err = retryRateLimited(direct, func() error { calls++; return igdb.ErrBadRequest })
	assert.ErrorIs(t, err, igdb.ErrBadRequest)
	assert.Equal(t, 1, calls, "other errors are not retried")
}

func TestAdminCreate(t *testing.T) {
	m, api, db, now := newTestModule(t, nil)
	existing := makeSquad(t, m, api, db, "a", "b")
	member(api, "c", "role-eu")
	member(api, "d", "role-na")
	queue(t, m, api, db, now.Add(-time.Hour), []string{"q1"}, []int64{1}, "role-eu")
	users := func(uids ...string) []*discordgo.ApplicationCommandInteractionDataOption {
		var opts []*discordgo.ApplicationCommandInteractionDataOption
		for n, uid := range uids {
			opts = append(opts, &discordgo.ApplicationCommandInteractionDataOption{
				Name: fmt.Sprintf("user%d", n+1), Type: discordgo.ApplicationCommandOptionUser, Value: uid})
		}
		return opts
	}
	create := func(uids ...string) string {
		m.handleAdmin(nil, command("mod", discordgo.PermissionBanMembers, "squad-admin", "create", users(uids...)...))
		return editContent(api.lastEdit())
	}
	before, _ := db.ListSquads(guild)

	assert.Contains(t, create("c", "a"), "<@a> is already in **"+existing.Name+"**")
	assert.Contains(t, create("c", "ghost"), "<@ghost> isn't in the server")
	assert.Contains(t, create("c", "c"), "at least two different members")
	after, _ := db.ListSquads(guild)
	assert.Len(t, after, len(before), "a failed create makes nothing")

	out := create("c", "d", "q1")
	assert.Contains(t, out, "✅ Created **")
	sq, ok, _ := db.SquadForUser(guild, "q1")
	require.True(t, ok)
	assert.Equal(t, 3, sq.Members, "matching rules like region don't apply")
	q, _ := db.SquadQueue(guild)
	assert.Empty(t, q, "queued members leave the queue")
	var welcome string
	for _, s := range api.sent {
		if s.ChannelID == sq.ChannelID {
			welcome = s.Msg.Content
			break
		}
	}
	assert.Contains(t, welcome, "A moderator put this squad together.")
	log := modLogEntry(t, api)
	assert.Equal(t, "🧑‍🤝‍🧑 Squad Created", log.Title)
	assert.Contains(t, embedText(log), "By: <@mod>")
	assert.Contains(t, embedText(log), "Members: <@c> <@d> <@q1>")
	assert.Contains(t, embedText(log), "Squad: **"+sq.Name+"** (<#"+sq.ChannelID+">)")
}

// modLogEntry returns the last mod log embed.
func modLogEntry(t *testing.T, api *fakeAPI) *discordgo.MessageEmbed {
	t.Helper()
	for n := len(api.sent) - 1; n >= 0; n-- {
		if sm := api.sent[n]; sm.ChannelID == "modlog" {
			require.Empty(t, sm.Msg.Content, "mod log entries are embeds")
			require.Len(t, sm.Msg.Embeds, 1)
			return sm.Msg.Embeds[0]
		}
	}
	t.Fatal("no mod log entry")
	return nil
}

func embedText(e *discordgo.MessageEmbed) string {
	parts := []string{e.Title, e.Description}
	for _, f := range e.Fields {
		parts = append(parts, f.Name+": "+f.Value)
	}
	return strings.Join(parts, "\n")
}
