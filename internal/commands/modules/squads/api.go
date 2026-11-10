package squads

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"gamerpal/internal/igdbclient"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// discordAPI is every Discord call the module makes, so tests can fake them.
type discordAPI interface {
	Respond(i *discordgo.Interaction, resp *discordgo.InteractionResponse) error
	EditResponse(i *discordgo.Interaction, edit *discordgo.WebhookEdit) error
	Send(channelID string, msg *discordgo.MessageSend) (*discordgo.Message, error)
	Pin(channelID, messageID string) error
	Member(guildID, userID string) (*discordgo.Member, error)
	CreateRole(guildID string, params *discordgo.RoleParams) (*discordgo.Role, error)
	DeleteRole(guildID, roleID string) error
	AddRole(guildID, userID, roleID string) error
	RemoveRole(guildID, userID, roleID string) error
	CreateChannel(guildID string, data discordgo.GuildChannelCreateData) (*discordgo.Channel, error)
	EditChannelPerms(channelID string, overwrites []*discordgo.PermissionOverwrite) error
	DeleteChannel(channelID string) error
	DM(userID string, msg *discordgo.MessageSend) error
	BotID() string
	Channel(channelID string) (*discordgo.Channel, error)
	// LastHumanMessage returns when a non-bot last posted in a channel after
	// since (zero if nobody has).
	LastHumanMessage(channelID string, since time.Time) (time.Time, error)
}

type sessionAPI struct{ s *discordgo.Session }

func (a sessionAPI) Respond(i *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
	return a.s.InteractionRespond(i, resp)
}

func (a sessionAPI) EditResponse(i *discordgo.Interaction, edit *discordgo.WebhookEdit) error {
	_, err := a.s.InteractionResponseEdit(i, edit)
	return err
}

func (a sessionAPI) Send(channelID string, msg *discordgo.MessageSend) (*discordgo.Message, error) {
	return a.s.ChannelMessageSendComplex(channelID, msg)
}

func (a sessionAPI) Pin(channelID, messageID string) error {
	return a.s.ChannelMessagePin(channelID, messageID)
}

func (a sessionAPI) Member(guildID, userID string) (*discordgo.Member, error) {
	if a.s.State != nil {
		if m, err := a.s.State.Member(guildID, userID); err == nil && m != nil {
			return m, nil
		}
	}
	return a.s.GuildMember(guildID, userID)
}

func (a sessionAPI) CreateRole(guildID string, params *discordgo.RoleParams) (*discordgo.Role, error) {
	return a.s.GuildRoleCreate(guildID, params)
}

func (a sessionAPI) DeleteRole(guildID, roleID string) error {
	return a.s.GuildRoleDelete(guildID, roleID)
}

func (a sessionAPI) AddRole(guildID, userID, roleID string) error {
	return a.s.GuildMemberRoleAdd(guildID, userID, roleID)
}

func (a sessionAPI) RemoveRole(guildID, userID, roleID string) error {
	return a.s.GuildMemberRoleRemove(guildID, userID, roleID)
}

func (a sessionAPI) CreateChannel(guildID string, data discordgo.GuildChannelCreateData) (*discordgo.Channel, error) {
	return a.s.GuildChannelCreateComplex(guildID, data)
}

func (a sessionAPI) EditChannelPerms(channelID string, overwrites []*discordgo.PermissionOverwrite) error {
	_, err := a.s.ChannelEdit(channelID, &discordgo.ChannelEdit{PermissionOverwrites: overwrites})
	return err
}

func (a sessionAPI) DeleteChannel(channelID string) error {
	_, err := a.s.ChannelDelete(channelID)
	return err
}

func (a sessionAPI) DM(userID string, msg *discordgo.MessageSend) error {
	ch, err := a.s.UserChannelCreate(userID)
	if err != nil {
		return err
	}
	_, err = a.s.ChannelMessageSendComplex(ch.ID, msg)
	return err
}

func (a sessionAPI) Channel(channelID string) (*discordgo.Channel, error) {
	if a.s.State != nil {
		if c, err := a.s.State.Channel(channelID); err == nil && c != nil {
			return c, nil
		}
	}
	return a.s.Channel(channelID)
}

// maxHistoryPages bounds how far back LastHumanMessage reads (100 messages a
// page) before giving up with an error.
const maxHistoryPages = 20

var errHistoryTooLong = errors.New("too many bot messages to find the last human one")

func (a sessionAPI) LastHumanMessage(channelID string, since time.Time) (time.Time, error) {
	before := ""
	for range maxHistoryPages {
		msgs, err := a.s.ChannelMessages(channelID, 100, before, "", "")
		if err != nil {
			return time.Time{}, err
		}
		for _, msg := range msgs {
			if !msg.Timestamp.After(since) {
				return time.Time{}, nil
			}
			if msg.Author != nil && !msg.Author.Bot {
				return msg.Timestamp, nil
			}
		}
		if len(msgs) < 100 {
			return time.Time{}, nil
		}
		before = msgs[len(msgs)-1].ID
	}
	return time.Time{}, errHistoryTooLong
}

func (a sessionAPI) BotID() string {
	if a.s.State != nil && a.s.State.User != nil {
		return a.s.State.User.ID
	}
	return ""
}

// discordCode returns the Discord JSON error code of a REST error, or 0.
func discordCode(err error) int {
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Message != nil {
		return rest.Message.Code
	}
	return 0
}

// gameInfo is one IGDB game as the picker shows and stores it.
type gameInfo struct {
	ID   int64
	Name string
	Year int
	Tags []string
}

func (g gameInfo) label() string {
	if g.Year > 0 {
		return fmt.Sprintf("%s (%d)", g.Name, g.Year)
	}
	return g.Name
}

// gameSource searches IGDB; faked in tests.
type gameSource interface {
	Search(query string) ([]gameInfo, error)
	Get(id int64) (gameInfo, error)
}

// igdbSource looks games up on IGDB and resolves their genre and theme IDs
// to names, loading the (small, stable) name lists once.
type igdbSource struct {
	client *igdbclient.Client
	mu     sync.Mutex
	tags   map[string]string // "g:<id>" / "t:<id>" -> name
}

func unixYear(sec int64) int { return time.Unix(sec, 0).UTC().Year() }

var gameFields = igdb.SetFields("id", "name", "first_release_date", "genres", "themes")

// skippedTags are too broad to say anything about someone's taste.
var skippedTags = map[string]bool{"Indie": true}

func (src *igdbSource) Search(query string) ([]gameInfo, error) {
	var games []*igdb.Game
	err := src.client.Retry(func() error {
		svc, err := src.client.Games()
		if err != nil {
			return err
		}
		games, err = svc.Search(query, gameFields, igdb.SetLimit(20))
		if errors.Is(err, igdb.ErrNoResults) {
			games, err = nil, nil
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return src.convert(games), nil
}

func (src *igdbSource) Get(id int64) (gameInfo, error) {
	if id <= 0 || id > math.MaxInt32 {
		return gameInfo{}, fmt.Errorf("game %d not found", id)
	}
	ids := []int{int(id)}
	var games []*igdb.Game
	err := src.client.Retry(func() error {
		svc, err := src.client.Games()
		if err != nil {
			return err
		}
		games, err = svc.List(ids, gameFields)
		return err
	})
	if err != nil {
		return gameInfo{}, err
	}
	out := src.convert(games)
	if len(out) == 0 {
		return gameInfo{}, fmt.Errorf("game %d not found", id)
	}
	return out[0], nil
}

func (src *igdbSource) convert(games []*igdb.Game) []gameInfo {
	tags := src.loadTags()
	seen := map[int64]bool{}
	var out []gameInfo
	for _, g := range games {
		if g == nil || g.Name == "" || seen[int64(g.ID)] {
			continue
		}
		seen[int64(g.ID)] = true
		info := gameInfo{ID: int64(g.ID), Name: g.Name}
		if g.FirstReleaseDate > 0 {
			info.Year = unixYear(int64(g.FirstReleaseDate))
		}
		for _, id := range g.Genres {
			if name := tags[fmt.Sprintf("g:%d", id)]; name != "" && !skippedTags[name] {
				info.Tags = append(info.Tags, name)
			}
		}
		for _, id := range g.Themes {
			if name := tags[fmt.Sprintf("t:%d", id)]; name != "" && !skippedTags[name] {
				info.Tags = append(info.Tags, name)
			}
		}
		out = append(out, info)
	}
	return out
}

// loadTags fetches the genre and theme name lists. A failure leaves them
// empty so the next lookup retries; games still match on exact picks.
func (src *igdbSource) loadTags() map[string]string {
	src.mu.Lock()
	defer src.mu.Unlock()
	if len(src.tags) > 0 {
		return src.tags
	}
	tags := map[string]string{}
	err := src.client.Retry(func() error {
		gs, err := src.client.Genres()
		if err != nil {
			return err
		}
		genres, err := gs.Index(igdb.SetFields("id", "name"), igdb.SetLimit(500))
		if err != nil {
			return err
		}
		ts, err := src.client.Themes()
		if err != nil {
			return err
		}
		themes, err := ts.Index(igdb.SetFields("id", "name"), igdb.SetLimit(500))
		if err != nil {
			return err
		}
		for _, g := range genres {
			tags[fmt.Sprintf("g:%d", g.ID)] = g.Name
		}
		for _, t := range themes {
			tags[fmt.Sprintf("t:%d", t.ID)] = t.Name
		}
		return nil
	})
	if err == nil {
		src.tags = tags
	}
	return tags
}
