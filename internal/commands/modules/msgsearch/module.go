package msgsearch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// exportTimeout stays under the 15 minute interaction token lifetime.
const exportTimeout = 13 * time.Minute

// Module provides /search, a moderator export of Discord's guild message search.
type Module struct {
	config  *config.Config
	running sync.Map // userID -> struct{}; one export per moderator at a time
}

// New creates the search module.
func New(deps *types.Dependencies) *Module {
	return &Module{config: deps.Config}
}

// Service returns nil; /search has no background work.
func (m *Module) Service() types.ModuleService { return nil }

func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	m.config = deps.Config
	var modPerms int64 = discordgo.PermissionBanMembers
	minPage := 1.0

	cmds["search"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "search",
			Description:              "Search server messages and export every result page as a spreadsheet (CSV)",
			DefaultMemberPermissions: &modPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionString, Name: "content", Description: "Text to search for", MaxLength: 1024},
				{Type: discordgo.ApplicationCommandOptionUser, Name: "author", Description: "Only messages sent by this user"},
				{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Only messages in this channel"},
				{Type: discordgo.ApplicationCommandOptionUser, Name: "mentions", Description: "Only messages that mention this user"},
				{
					Type: discordgo.ApplicationCommandOptionString, Name: "has", Description: "Only messages that have this",
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "link", Value: "link"}, {Name: "embed", Value: "embed"}, {Name: "file", Value: "file"},
						{Name: "image", Value: "image"}, {Name: "video", Value: "video"}, {Name: "sound", Value: "sound"},
						{Name: "sticker", Value: "sticker"}, {Name: "poll", Value: "poll"}, {Name: "forward", Value: "snapshot"},
					},
				},
				{
					Type: discordgo.ApplicationCommandOptionString, Name: "author_type", Description: "Only messages from this kind of author",
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "users", Value: "user"}, {Name: "bots", Value: "bot"}, {Name: "webhooks", Value: "webhook"},
					},
				},
				{Type: discordgo.ApplicationCommandOptionBoolean, Name: "pinned", Description: "Only pinned (true) or unpinned (false) messages"},
				{Type: discordgo.ApplicationCommandOptionString, Name: "after", Description: "From this day, inclusive (YYYY-MM-DD, UTC)", MinLength: intPtr(10), MaxLength: 10},
				{Type: discordgo.ApplicationCommandOptionString, Name: "before", Description: "Up to this day, inclusive (YYYY-MM-DD, UTC)", MinLength: intPtr(10), MaxLength: 10},
				{
					Type: discordgo.ApplicationCommandOptionString, Name: "sort", Description: "Result order (default: newest first)",
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "newest first", Value: "newest"}, {Name: "oldest first", Value: "oldest"}, {Name: "most relevant", Value: "relevance"},
					},
				},
				{Type: discordgo.ApplicationCommandOptionInteger, Name: "page_start", Description: "First result page to export (25 per page, default 1)", MinValue: &minPage, MaxValue: MaxPages},
				{Type: discordgo.ApplicationCommandOptionInteger, Name: "page_end", Description: "Last result page to export (default: the last page)", MinValue: &minPage, MaxValue: MaxPages},
				{Type: discordgo.ApplicationCommandOptionBoolean, Name: "include_nsfw", Description: "Include age-restricted channels (default false)"},
			},
		},
		HandlerFunc: m.handleSearch,
	}
}

func intPtr(v int) *int { return &v }

type searchRequest struct {
	Params    searchParams
	PageStart int
	PageEnd   int
	Summary   []string // human-readable filters for messages and the audit log
}

func parseRequest(data discordgo.ApplicationCommandInteractionData) (*searchRequest, error) {
	req := &searchRequest{PageStart: 1}
	var after, before string
	for _, opt := range data.Options {
		switch opt.Name {
		case "content":
			req.Params.Content = opt.StringValue()
			req.Summary = append(req.Summary, fmt.Sprintf("content: `%s`", strings.ReplaceAll(req.Params.Content, "`", "'")))
		case "author":
			req.Params.AuthorID = fmt.Sprint(opt.Value)
			req.Summary = append(req.Summary, fmt.Sprintf("author: <@%s>", req.Params.AuthorID))
		case "channel":
			req.Params.ChannelID = fmt.Sprint(opt.Value)
			req.Summary = append(req.Summary, fmt.Sprintf("channel: <#%s>", req.Params.ChannelID))
		case "mentions":
			req.Params.MentionsID = fmt.Sprint(opt.Value)
			req.Summary = append(req.Summary, fmt.Sprintf("mentions: <@%s>", req.Params.MentionsID))
		case "has":
			req.Params.Has = opt.StringValue()
			req.Summary = append(req.Summary, "has: "+req.Params.Has)
		case "author_type":
			req.Params.AuthorType = opt.StringValue()
			req.Summary = append(req.Summary, "author type: "+req.Params.AuthorType)
		case "pinned":
			v := opt.BoolValue()
			req.Params.Pinned = &v
			req.Summary = append(req.Summary, fmt.Sprintf("pinned: %t", v))
		case "after":
			after = strings.TrimSpace(opt.StringValue())
			req.Summary = append(req.Summary, "after: "+after)
		case "before":
			before = strings.TrimSpace(opt.StringValue())
			req.Summary = append(req.Summary, "before: "+before)
		case "sort":
			switch opt.StringValue() {
			case "oldest":
				req.Params.SortBy, req.Params.SortOrder = "timestamp", "asc"
			case "relevance":
				req.Params.SortBy = "relevance"
			default:
				req.Params.SortBy, req.Params.SortOrder = "timestamp", "desc"
			}
			req.Summary = append(req.Summary, "sort: "+opt.StringValue())
		case "page_start":
			req.PageStart = int(opt.IntValue())
		case "page_end":
			req.PageEnd = int(opt.IntValue())
		case "include_nsfw":
			req.Params.IncludeNSFW = opt.BoolValue()
			if req.Params.IncludeNSFW {
				req.Summary = append(req.Summary, "including NSFW channels")
			}
		}
	}

	minID, maxID, err := dayBounds(after, before)
	if err != nil {
		return nil, err
	}
	req.Params.MinID, req.Params.MaxID = minID, maxID

	if req.PageStart < 1 || req.PageStart > MaxPages {
		return nil, fmt.Errorf("page_start must be between 1 and %d", MaxPages)
	}
	if req.PageEnd != 0 && req.PageEnd < req.PageStart {
		return nil, fmt.Errorf("page_end (%d) must be greater than or equal to page_start (%d)", req.PageEnd, req.PageStart)
	}
	return req, nil
}

func (m *Module) handleSearch(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !utils.HasBanPermissions(i) {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "❌ You need Ban Members to use this command.", Flags: discordgo.MessageFlagsEphemeral},
		})
		return
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	}); err != nil {
		m.config.Logger.Errorf("search: defer failed: %v", err)
		return
	}

	userID := i.Member.User.ID
	req, err := parseRequest(i.ApplicationCommandData())
	if err != nil {
		m.edit(s, i, "❌ "+err.Error())
		return
	}

	if _, busy := m.running.LoadOrStore(userID, struct{}{}); busy {
		m.edit(s, i, "⏳ You already have a search export running. Wait for it to finish first.")
		return
	}
	defer m.running.Delete(userID)

	isAdmin := i.Member.Permissions&discordgo.PermissionAdministrator != 0
	channels := newChannelResolver(s)
	canView := func(channelID string) bool {
		return isAdmin || channels.memberCanRead(userID, channelID)
	}
	if req.Params.ChannelID != "" && !canView(req.Params.ChannelID) {
		m.edit(s, i, "🤷 No messages matched that search.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), exportTimeout)
	defer cancel()

	m.edit(s, i, "🔎 Searching…")
	var lastEdit time.Time
	progress := func(page, _ int) {
		if time.Since(lastEdit) < 3*time.Second {
			return
		}
		lastEdit = time.Now()
		m.edit(s, i, fmt.Sprintf("🔎 Fetching page %d…", page))
	}

	fetcher := pageFetcher{search: discordSearch(s), sleep: sleepCtx}
	res, err := fetcher.collect(ctx, i.GuildID, req.Params, req.PageStart, req.PageEnd, progress)
	if err != nil {
		m.edit(s, i, "❌ Search failed: "+err.Error())
		return
	}
	if res.TotalResults == 0 || len(res.Messages) == 0 {
		m.edit(s, i, "🤷 No messages matched that search.")
		return
	}

	channels.addThreads(res.Threads)
	// Results the moderator can't read are dropped silently: nothing shown to
	// them (counts, totals, page numbers) may reveal that anything was removed.
	visible := filterVisible(res.Messages, canView)
	if len(visible) == 0 {
		m.edit(s, i, "🤷 No messages matched that search.")
		return
	}

	data, err := buildCSV(i.GuildID, visible, channels.info)
	if err != nil {
		m.edit(s, i, "❌ Failed to build the spreadsheet: "+err.Error())
		return
	}
	file, err := packageExport(fmt.Sprintf("search-%s.csv", time.Now().UTC().Format("20060102-150405")), data)
	if err != nil {
		m.edit(s, i, "❌ "+err.Error())
		return
	}

	summary := exportSummary(req, res, len(visible))
	content := summary
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &content,
		Files:   []*discordgo.File{file},
	}); err != nil {
		m.config.Logger.Errorf("search: upload failed: %v", err)
		m.edit(s, i, "❌ Couldn't upload the export: "+err.Error())
		return
	}

	_ = utils.LogToChannel(m.config, s, fmt.Sprintf("🔎 <@%s> exported %d search results (pages %d-%d).\nFilters: %s",
		userID, len(visible), res.FirstPage, res.LastPage, filterList(req.Summary)))
}

func filterList(summary []string) string {
	if len(summary) == 0 {
		return "none"
	}
	return strings.Join(summary, ", ")
}

// exportSummary must not include totals or page counts from Discord, since
// those include results the moderator isn't allowed to see.
func exportSummary(req *searchRequest, res *exportResult, rows int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "✅ Exported **%d** messages from pages **%d-%d**.\n", rows, res.FirstPage, res.LastPage)
	fmt.Fprintf(&b, "Filters: %s", filterList(req.Summary))
	if res.Truncated {
		fmt.Fprintf(&b, "\n⚠️ Hit the time limit before the last page. Run again with `page_start:%d` to continue.", res.LastPage+1)
	}
	return b.String()
}

func filterVisible(msgs []*discordgo.Message, canView func(string) bool) (visible []*discordgo.Message) {
	allowed := map[string]bool{}
	for _, msg := range msgs {
		ok, seen := allowed[msg.ChannelID]
		if !seen {
			ok = canView(msg.ChannelID)
			allowed[msg.ChannelID] = ok
		}
		if ok {
			visible = append(visible, msg)
		}
	}
	return visible
}

func (m *Module) edit(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
		m.config.Logger.Errorf("search: edit failed: %v", err)
	}
}

// channelResolver caches channel lookups (state, then search-response threads, then REST)
// and per-user read access. Lookups are injectable for tests.
type channelResolver struct {
	lookup       func(channelID string) *discordgo.Channel
	permissions  func(userID, channelID string) (int64, error)
	threadMember func(threadID, userID string) bool

	cache        map[string]*discordgo.Channel
	perms        map[string]int64
	permsErr     map[string]bool
	threadAccess map[string]bool
}

func newChannelResolver(s *discordgo.Session) *channelResolver {
	r := newResolver()
	r.lookup = func(id string) *discordgo.Channel {
		var ch *discordgo.Channel
		if s.State != nil {
			ch, _ = s.State.Channel(id)
		}
		if ch == nil {
			ch, _ = s.Channel(id)
		}
		return ch
	}
	r.permissions = func(userID, channelID string) (int64, error) {
		return s.UserChannelPermissions(userID, channelID)
	}
	r.threadMember = func(threadID, userID string) bool {
		// 404 (not a member) and any other error both deny.
		tm, err := s.ThreadMember(threadID, userID, false)
		return err == nil && tm != nil
	}
	return r
}

func newResolver() *channelResolver {
	return &channelResolver{
		cache:        map[string]*discordgo.Channel{},
		perms:        map[string]int64{},
		permsErr:     map[string]bool{},
		threadAccess: map[string]bool{},
	}
}

func (r *channelResolver) addThreads(threads map[string]*discordgo.Channel) {
	for id, th := range threads {
		if r.cache[id] == nil {
			r.cache[id] = th
		}
	}
}

func (r *channelResolver) channel(id string) *discordgo.Channel {
	if ch, ok := r.cache[id]; ok && ch != nil {
		return ch
	}
	ch := r.lookup(id)
	r.cache[id] = ch
	return ch
}

func (r *channelResolver) info(id string) channelInfo {
	ch := r.channel(id)
	if ch == nil {
		return channelInfo{}
	}
	info := channelInfo{Name: ch.Name}
	if ch.IsThread() && ch.ParentID != "" {
		if parent := r.channel(ch.ParentID); parent != nil {
			info.ParentName = parent.Name
		}
	}
	return info
}

func (r *channelResolver) permsFor(userID, channelID string) (int64, bool) {
	if p, ok := r.perms[channelID]; ok {
		return p, true
	}
	if r.permsErr[channelID] {
		return 0, false
	}
	p, err := r.permissions(userID, channelID)
	if err != nil {
		r.permsErr[channelID] = true
		return 0, false
	}
	r.perms[channelID] = p
	return p, true
}

// memberCanRead reports whether the user can view and read history in a channel,
// mirroring Discord's rules. Threads use their parent channel's permissions;
// private threads additionally need Manage Threads or thread membership.
// Anything that can't be resolved is denied.
func (r *channelResolver) memberCanRead(userID, channelID string) bool {
	ch := r.channel(channelID)
	if ch == nil {
		return false
	}
	permID := channelID
	if ch.IsThread() {
		if ch.ParentID == "" {
			return false
		}
		permID = ch.ParentID
	}
	p, ok := r.permsFor(userID, permID)
	if !ok {
		return false
	}
	if p&discordgo.PermissionAdministrator != 0 {
		return true
	}
	const need = discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory
	if p&need != need {
		return false
	}
	if ch.Type != discordgo.ChannelTypeGuildPrivateThread || p&discordgo.PermissionManageThreads != 0 {
		return true
	}
	allowed, cached := r.threadAccess[channelID]
	if !cached {
		allowed = r.threadMember(channelID, userID)
		r.threadAccess[channelID] = allowed
	}
	return allowed
}
