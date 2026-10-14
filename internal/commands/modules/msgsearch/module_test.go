package msgsearch

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeIndex serves a fixed list of message IDs through the search API's offset paging.
type fakeIndex struct {
	total    int // reported total_results
	ids      []string
	offsets  []int
	notReady int // number of 202 responses before serving
	hidden   func(i int) bool
}

func (f *fakeIndex) search(_ context.Context, _ string, q url.Values) (*searchResponse, error) {
	if f.notReady > 0 {
		f.notReady--
		return nil, &indexNotReadyError{RetryAfter: 2 * time.Second}
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	f.offsets = append(f.offsets, offset)
	resp := &searchResponse{TotalResults: f.total}
	for i := offset; i < offset+PageSize && i < len(f.ids); i++ {
		ch := "c1"
		if f.hidden != nil && f.hidden(i) {
			ch = "secret"
		}
		resp.Messages = append(resp.Messages, []*discordgo.Message{{ID: f.ids[i], ChannelID: ch}})
	}
	return resp, nil
}

func newFake(n int) *fakeIndex {
	f := &fakeIndex{total: n}
	for i := 0; i < n; i++ {
		f.ids = append(f.ids, fmt.Sprintf("m%d", i))
	}
	return f
}

func noSleep(context.Context, time.Duration) error { return nil }

func collectAll(f *fakeIndex, start, end int) (*exportResult, error) {
	return pageFetcher{search: f.search, sleep: noSleep}.collect(context.Background(), "g", searchParams{}, start, end, nil, nil)
}

func hideSecret(msgs []*discordgo.Message, _ []*discordgo.Channel) []*discordgo.Message {
	return filterVisible(msgs, func(id string) bool { return id != "secret" })
}

func TestCollectAllPages(t *testing.T) {
	f := newFake(60)
	res, err := collectAll(f, 1, 0)
	require.NoError(t, err)
	assert.Len(t, res.Messages, 60)
	assert.Equal(t, 1, res.FirstPage)
	assert.Equal(t, 3, res.LastPage)
	assert.Equal(t, []int{0, 25, 50}, f.offsets)
	assert.False(t, res.Truncated)
}

func TestCollectPageRange(t *testing.T) {
	f := newFake(200)
	res, err := collectAll(f, 2, 3)
	require.NoError(t, err)
	assert.Equal(t, []int{0, 25, 50}, f.offsets, "visible paging always starts at Discord's first page and stops once page_end is filled")
	assert.Len(t, res.Messages, 50)
	assert.Equal(t, "m25", res.Messages[0].ID)
	assert.Equal(t, 2, res.FirstPage)
	assert.Equal(t, 3, res.LastPage)
}

func TestCollectClampsPageEndToLastPage(t *testing.T) {
	f := newFake(30)
	res, err := collectAll(f, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, 2, res.LastPage)
	assert.Len(t, res.Messages, 30)
}

func TestCollectPageStartPastEndIsEmptyNotError(t *testing.T) {
	f := newFake(30)
	res, err := collectAll(f, 5, 0)
	require.NoError(t, err)
	assert.Empty(t, res.Messages)
	assert.Equal(t, []int{0}, f.offsets)
}

func TestCollectPagesOverVisibleResultsOnly(t *testing.T) {
	// Every 3rd result is hidden: 150 raw results -> 100 visible.
	f := newFake(150)
	f.hidden = func(i int) bool { return i%3 == 0 }
	res, err := pageFetcher{search: f.search, sleep: noSleep}.collect(context.Background(), "g", searchParams{}, 2, 3, hideSecret, nil)
	require.NoError(t, err)
	require.Len(t, res.Messages, 50, "pages 2-3 are full pages of visible results")
	for _, m := range res.Messages {
		assert.Equal(t, "c1", m.ChannelID)
	}
	// Visible result #25 (0-based) is raw index 38.
	assert.Equal(t, "m38", res.Messages[0].ID)
	assert.Equal(t, 2, res.FirstPage)
	assert.Equal(t, 3, res.LastPage)

	all, err := pageFetcher{search: newFake(150).search, sleep: noSleep}.collect(context.Background(), "g", searchParams{}, 1, 0, hideSecret, nil)
	require.NoError(t, err)
	assert.Len(t, all.Messages, 150)

	f = newFake(150)
	f.hidden = func(i int) bool { return i%3 == 0 }
	all, err = pageFetcher{search: f.search, sleep: noSleep}.collect(context.Background(), "g", searchParams{}, 1, 0, hideSecret, nil)
	require.NoError(t, err)
	assert.Len(t, all.Messages, 100)
	assert.Equal(t, 4, all.LastPage, "page count reflects visible results, not Discord's total")
}

func TestCollectAllHiddenIsEmpty(t *testing.T) {
	f := newFake(60)
	f.hidden = func(int) bool { return true }
	res, err := pageFetcher{search: f.search, sleep: noSleep}.collect(context.Background(), "g", searchParams{}, 1, 0, hideSecret, nil)
	require.NoError(t, err)
	assert.Empty(t, res.Messages)
}

func TestCollectStopsOnEmptyPageWhenTotalOverstated(t *testing.T) {
	f := newFake(30)
	f.total = 500
	res, err := collectAll(f, 1, 0)
	require.NoError(t, err)
	assert.Len(t, res.Messages, 30)
	assert.Equal(t, []int{0, 25, 50}, f.offsets)
}

func TestCollectCapsAtMaxPages(t *testing.T) {
	assert.Equal(t, 400, MaxPages)
	assert.Equal(t, 400, totalPages(1_000_000))
	assert.Equal(t, 1, totalPages(1))
	assert.Equal(t, 0, totalPages(0))
}

func TestCollectRetriesWhileIndexing(t *testing.T) {
	f := newFake(10)
	f.notReady = 3
	var waits []time.Duration
	sleep := func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	res, err := pageFetcher{search: f.search, sleep: sleep}.collect(context.Background(), "g", searchParams{}, 1, 0, nil, nil)
	require.NoError(t, err)
	assert.Len(t, res.Messages, 10)
	assert.Equal(t, []time.Duration{2 * time.Second, 2 * time.Second, 2 * time.Second}, waits)
}

func TestCollectGivesUpWhenNeverIndexed(t *testing.T) {
	f := newFake(10)
	f.notReady = 1000
	_, err := collectAll(f, 1, 0)
	assert.ErrorContains(t, err, "still indexing")
}

func TestCollectTruncatesToWholeVisiblePages(t *testing.T) {
	// 1 in 5 hidden: after 3 raw pages there are 60 visible rows -> 2 whole pages kept.
	f := newFake(500)
	f.hidden = func(i int) bool { return i%5 == 0 }
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	progress := func(rows int) {
		calls++
		if calls == 3 {
			cancel()
		}
	}
	res, err := pageFetcher{search: f.search, sleep: noSleep}.collect(ctx, "g", searchParams{}, 1, 0, hideSecret, progress)
	require.NoError(t, err)
	assert.True(t, res.Truncated)
	assert.Len(t, res.Messages, 50)
	assert.Equal(t, 2, res.LastPage)
}

func TestCollectTruncatedBeforePageStartIsEmpty(t *testing.T) {
	f := newFake(500)
	ctx, cancel := context.WithCancel(context.Background())
	res, err := pageFetcher{search: f.search, sleep: noSleep}.collect(ctx, "g", searchParams{}, 10, 0, nil, func(int) { cancel() })
	require.NoError(t, err)
	assert.True(t, res.Truncated)
	assert.Empty(t, res.Messages)
}

func TestCollectDedupes(t *testing.T) {
	search := func(_ context.Context, _ string, q url.Values) (*searchResponse, error) {
		return &searchResponse{TotalResults: 50, Messages: [][]*discordgo.Message{{{ID: "same"}}}}, nil
	}
	res, err := pageFetcher{search: search, sleep: noSleep}.collect(context.Background(), "g", searchParams{}, 1, 0, nil, nil)
	require.NoError(t, err)
	assert.Len(t, res.Messages, 1)
}

func TestValuesEncodesFilters(t *testing.T) {
	pinned := false
	q := searchParams{
		Content: "hello world", AuthorID: "1", ChannelID: "2", MentionsID: "3", Has: "link",
		AuthorType: "user", Pinned: &pinned, MinID: "4", MaxID: "5", SortBy: "timestamp", SortOrder: "asc", IncludeNSFW: true,
	}.values(50)
	assert.Equal(t, "25", q.Get("limit"))
	assert.Equal(t, "50", q.Get("offset"))
	assert.Equal(t, "hello world", q.Get("content"))
	assert.Equal(t, "1", q.Get("author_id"))
	assert.Equal(t, "2", q.Get("channel_id"))
	assert.Equal(t, "3", q.Get("mentions"))
	assert.Equal(t, "link", q.Get("has"))
	assert.Equal(t, "user", q.Get("author_type"))
	assert.Equal(t, "false", q.Get("pinned"))
	assert.Equal(t, "4", q.Get("min_id"))
	assert.Equal(t, "5", q.Get("max_id"))
	assert.Equal(t, "asc", q.Get("sort_order"))
	assert.Equal(t, "true", q.Get("include_nsfw"))

	empty := searchParams{}.values(0)
	assert.Equal(t, url.Values{"limit": {"25"}}, empty)
}

func TestDayBounds(t *testing.T) {
	minID, maxID, err := dayBounds("2026-10-01", "2026-10-01")
	require.NoError(t, err)

	min, _ := strconv.ParseUint(minID, 10, 64)
	max, _ := strconv.ParseUint(maxID, 10, 64)
	dayStart := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	inDay, _ := strconv.ParseUint(snowflakeAt(dayStart.Add(12*time.Hour)), 10, 64)
	startOfDay, _ := strconv.ParseUint(snowflakeAt(dayStart), 10, 64)
	nextDay, _ := strconv.ParseUint(snowflakeAt(dayStart.AddDate(0, 0, 1)), 10, 64)

	assert.True(t, min < startOfDay, "after is inclusive")
	assert.True(t, inDay > min && inDay < max)
	assert.Equal(t, nextDay, max, "before is inclusive of the whole day")

	ts, err := discordgo.SnowflakeTimestamp(maxID)
	require.NoError(t, err)
	assert.Equal(t, dayStart.AddDate(0, 0, 1), ts.UTC())

	_, _, err = dayBounds("10/01/2026", "")
	assert.ErrorContains(t, err, "YYYY-MM-DD")
	_, _, err = dayBounds("2026-10-05", "2026-10-01")
	assert.Error(t, err)
}

func TestSanitizeCell(t *testing.T) {
	assert.Equal(t, "'=HYPERLINK(\"x\")", sanitizeCell("=HYPERLINK(\"x\")"))
	assert.Equal(t, "'+1", sanitizeCell("+1"))
	assert.Equal(t, "'-1", sanitizeCell("-1"))
	assert.Equal(t, "'@everyone", sanitizeCell("@everyone"))
	assert.Equal(t, "hello", sanitizeCell("hello"))
	assert.Equal(t, "", sanitizeCell(""))
}

func TestBuildCSV(t *testing.T) {
	edited := time.Date(2026, 10, 2, 3, 4, 5, 0, time.UTC)
	msgs := []*discordgo.Message{
		{
			ID: "100", ChannelID: "t1", Content: "=cmd|' /C calc'!A0, \"quoted\"\nnew line 🫃",
			Timestamp:        time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
			EditedTimestamp:  &edited,
			Author:           &discordgo.User{ID: "u1", Username: "noodle", GlobalName: "Noodle"},
			Attachments:      []*discordgo.MessageAttachment{{URL: "https://cdn/a.png"}, {URL: "https://cdn/b.png"}},
			Embeds:           []*discordgo.MessageEmbed{{URL: "https://example.com"}},
			StickerItems:     []*discordgo.StickerItem{{Name: "wave"}},
			MessageReference: &discordgo.MessageReference{MessageID: "99"},
			Pinned:           true,
		},
		{
			ID: "101", ChannelID: "c1",
			Author:           &discordgo.User{ID: "b1", Username: "bot", Bot: true},
			MessageSnapshots: []discordgo.MessageSnapshot{{Message: &discordgo.Message{Content: "fwd text"}}},
		},
	}
	names := func(id string) channelInfo {
		if id == "t1" {
			return channelInfo{Name: "my-thread", ParentName: "lfg"}
		}
		return channelInfo{Name: "general"}
	}
	data, err := buildCSV("g1", msgs, names)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(data, []byte("\ufeff")))

	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\ufeff")))).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 3)
	assert.Equal(t, csvHeader, rows[0])

	col := func(row []string, name string) string {
		for i, h := range csvHeader {
			if h == name {
				return row[i]
			}
		}
		t.Fatalf("no column %s", name)
		return ""
	}
	r := rows[1]
	assert.Equal(t, "100", col(r, "message_id"))
	assert.Equal(t, "2026-10-01 12:00:00", col(r, "timestamp_utc"))
	assert.Equal(t, "2026-10-02 03:04:05", col(r, "edited_utc"))
	assert.Equal(t, "Noodle", col(r, "author_display_name"))
	assert.Equal(t, "false", col(r, "author_is_bot"))
	assert.Equal(t, "my-thread", col(r, "channel_name"))
	assert.Equal(t, "lfg", col(r, "parent_channel_name"))
	assert.True(t, strings.HasPrefix(col(r, "content"), "'=cmd"))
	assert.Contains(t, col(r, "content"), "\"quoted\"\nnew line 🫃")
	assert.Equal(t, "https://cdn/a.png\nhttps://cdn/b.png", col(r, "attachments"))
	assert.Equal(t, "https://example.com", col(r, "embeds"))
	assert.Equal(t, "wave", col(r, "stickers"))
	assert.Equal(t, "99", col(r, "reply_to_message_id"))
	assert.Equal(t, "true", col(r, "pinned"))
	assert.Equal(t, "https://discord.com/channels/g1/t1/100", col(r, "jump_url"))

	r = rows[2]
	assert.Equal(t, "true", col(r, "author_is_bot"))
	assert.Equal(t, "[forwarded] fwd text", col(r, "content"))
	assert.Equal(t, "", col(r, "edited_utc"))
}

func TestPackageExport(t *testing.T) {
	small, err := packageExport("x.csv", []byte("a,b\n"))
	require.NoError(t, err)
	assert.Equal(t, "x.csv", small.Name)

	big := bytes.Repeat([]byte("same row,same row\n"), (maxUploadBytes/18)+10)
	zipped, err := packageExport("x.csv", big)
	require.NoError(t, err)
	assert.Equal(t, "x.zip", zipped.Name)
	assert.Equal(t, "application/zip", zipped.ContentType)
}

func TestFilterVisible(t *testing.T) {
	msgs := []*discordgo.Message{{ID: "1", ChannelID: "ok"}, {ID: "2", ChannelID: "secret"}, {ID: "3", ChannelID: "ok"}}
	calls := 0
	visible := filterVisible(msgs, func(id string) bool { calls++; return id == "ok" })
	assert.Len(t, visible, 2)
	assert.Equal(t, 2, calls, "permission lookups are cached per channel")
}

func TestParseRequest(t *testing.T) {
	data := discordgo.ApplicationCommandInteractionData{Options: []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "content", Type: discordgo.ApplicationCommandOptionString, Value: "gg"},
		{Name: "author", Type: discordgo.ApplicationCommandOptionUser, Value: "42"},
		{Name: "sort", Type: discordgo.ApplicationCommandOptionString, Value: "oldest"},
		{Name: "page_start", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(3)},
		{Name: "page_end", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(5)},
		{Name: "after", Type: discordgo.ApplicationCommandOptionString, Value: "2026-01-01"},
	}}
	req, err := parseRequest(data)
	require.NoError(t, err)
	assert.Equal(t, "gg", req.Params.Content)
	assert.Equal(t, "42", req.Params.AuthorID)
	assert.Equal(t, "timestamp", req.Params.SortBy)
	assert.Equal(t, "asc", req.Params.SortOrder)
	assert.Equal(t, 3, req.PageStart)
	assert.Equal(t, 5, req.PageEnd)
	assert.NotEmpty(t, req.Params.MinID)

	bad := discordgo.ApplicationCommandInteractionData{Options: []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "page_start", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(5)},
		{Name: "page_end", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(2)},
	}}
	_, err = parseRequest(bad)
	assert.ErrorContains(t, err, "page_end")
}

func TestRegister(t *testing.T) {
	cmds := map[string]*types.Command{}
	deps := &types.Dependencies{Config: config.NewMockConfig(nil)}
	New(deps).Register(cmds, deps)

	cmd := cmds["search"]
	require.NotNil(t, cmd)
	ac := cmd.ApplicationCommand
	require.NotNil(t, ac.DefaultMemberPermissions)
	assert.Equal(t, int64(discordgo.PermissionBanMembers), *ac.DefaultMemberPermissions)
	assert.LessOrEqual(t, len(ac.Options), 25)
	for _, o := range ac.Options {
		assert.False(t, o.Required, o.Name)
		assert.LessOrEqual(t, len(o.Description), 100, o.Name)
	}
}

func TestExportSummaryDoesNotLeakTotals(t *testing.T) {
	res := &exportResult{TotalResults: 9999, FirstPage: 1, LastPage: 3, Truncated: true}
	out := exportSummary(&searchRequest{}, res, 12)
	assert.Contains(t, out, "**12** messages")
	assert.Contains(t, out, "page_start:4")
	assert.NotContains(t, out, "9999")
	assert.NotContains(t, out, "400")
	assert.NotContains(t, strings.ToLower(out), "can't read")
}

func TestMemberCanRead(t *testing.T) {
	const read = discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory
	channels := map[string]*discordgo.Channel{
		"pub":       {ID: "pub", Type: discordgo.ChannelTypeGuildText},
		"secret":    {ID: "secret", Type: discordgo.ChannelTypeGuildText},
		"viewonly":  {ID: "viewonly", Type: discordgo.ChannelTypeGuildText},
		"pubthread": {ID: "pubthread", Type: discordgo.ChannelTypeGuildPublicThread, ParentID: "pub"},
		"privin":    {ID: "privin", Type: discordgo.ChannelTypeGuildPrivateThread, ParentID: "pub"},
		"privout":   {ID: "privout", Type: discordgo.ChannelTypeGuildPrivateThread, ParentID: "pub"},
		"secretpub": {ID: "secretpub", Type: discordgo.ChannelTypeGuildPublicThread, ParentID: "secret"},
		"orphan":    {ID: "orphan", Type: discordgo.ChannelTypeGuildPrivateThread},
		"errparent": {ID: "errparent", Type: discordgo.ChannelTypeGuildText},
	}
	newR := func(parentPerms map[string]int64) (*channelResolver, *int) {
		memberCalls := 0
		r := newResolver()
		r.lookup = func(id string) *discordgo.Channel { return channels[id] }
		r.permissions = func(_, id string) (int64, error) {
			if id == "errparent" {
				return 0, fmt.Errorf("boom")
			}
			return parentPerms[id], nil
		}
		r.threadMember = func(threadID, _ string) bool { memberCalls++; return threadID == "privin" }
		return r, &memberCalls
	}

	r, calls := newR(map[string]int64{"pub": read, "secret": 0, "viewonly": discordgo.PermissionViewChannel})
	cases := map[string]bool{
		"pub": true, "secret": false, "viewonly": false, "pubthread": true,
		"privin": true, "privout": false, "secretpub": false, "orphan": false,
		"errparent": false, "missing": false,
	}
	for id, want := range cases {
		assert.Equal(t, want, r.memberCanRead("u", id), id)
	}
	assert.False(t, r.memberCanRead("u", "privout"))
	assert.Equal(t, 2, *calls, "thread membership is checked once per private thread")

	r, calls = newR(map[string]int64{"pub": read | discordgo.PermissionManageThreads})
	assert.True(t, r.memberCanRead("u", "privout"), "Manage Threads can read every private thread")
	assert.Equal(t, 0, *calls)

	r, _ = newR(map[string]int64{"secret": discordgo.PermissionAdministrator})
	assert.True(t, r.memberCanRead("u", "secretpub"))
}

func TestAddThreadsFillsFailedLookups(t *testing.T) {
	r := newResolver()
	r.lookup = func(string) *discordgo.Channel { return nil }
	assert.Nil(t, r.channel("t1"))
	r.addThreads([]*discordgo.Channel{{ID: "t1", Name: "thread"}})
	assert.Equal(t, "thread", r.channel("t1").Name)
}
