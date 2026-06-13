package nineteeneightyfour

import (
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
)

const (
	testLogChannelID = "LOG_CH"
	testGuildID      = "G1"
	testBotID        = "BOT"
)

var fakeUser = discordgo.User{Username: "alice", ID: "U42"}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// captured holds what the module would have sent to Discord. Built by
// installing a stub dispatchLog in Module so handler logic can be exercised
// without any network IO.
type captured struct {
	channelID string
	payload   payload
}

type recorder struct {
	mu   sync.Mutex
	logs []captured
}

func (r *recorder) record(_ *discordgo.Session, channelID string, p payload) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, captured{channelID: channelID, payload: p})
	return nil
}

// newTestModule builds a Module wired with a stub dispatchLog and a config
// pre-set to use testLogChannelID. Returns the module and a recorder that
// captures every dispatched payload.
func newTestModule(t *testing.T) (*Module, *recorder) {
	t.Helper()
	cfg := config.NewMockConfig(map[string]any{
		"gamerpals_1984_log_channel_id": testLogChannelID,
	})
	m := New(&types.Dependencies{Config: cfg})
	r := &recorder{}
	m.dispatchLog = r.record
	return m, r
}

// newTestSession returns a discordgo.Session that has just enough state for
// our handlers: a populated State (so s.State.User and channel lookups work)
// and a fake bot user. All channels are added under testGuildID.
func newTestSession(channels ...*discordgo.Channel) *discordgo.Session {
	s := &discordgo.Session{State: discordgo.NewState()}
	s.State.User = &discordgo.User{ID: testBotID, Username: "bot"}
	_ = s.State.GuildAdd(&discordgo.Guild{ID: testGuildID})
	for _, c := range channels {
		if c.GuildID == "" {
			c.GuildID = testGuildID
		}
		_ = s.State.ChannelAdd(c)
	}
	return s
}

func textChannel(id, name string) *discordgo.Channel {
	return &discordgo.Channel{ID: id, Name: name, Type: discordgo.ChannelTypeGuildText}
}

func voiceChannel(id, name string) *discordgo.Channel {
	return &discordgo.Channel{ID: id, Name: name, Type: discordgo.ChannelTypeGuildVoice}
}

// assertPayloadWithinLimits enforces the Discord-side guarantees we promise:
// content fits in a Discord message, attachment count fits, each attachment
// fits, and no attachment has an empty name.
func assertPayloadWithinLimits(t *testing.T, p payload) {
	t.Helper()
	if len(p.content) > maxMessageContentChars {
		t.Errorf("content length %d exceeds maxMessageContentChars %d", len(p.content), maxMessageContentChars)
	}
	if len(p.content) > 2000 {
		t.Errorf("content length %d exceeds Discord hard limit 2000", len(p.content))
	}
	if len(p.files) > maxAttachmentsPerMessage {
		t.Errorf("file count %d exceeds maxAttachmentsPerMessage %d", len(p.files), maxAttachmentsPerMessage)
	}
	for _, f := range p.files {
		if len(f.content) > maxAttachmentBytes {
			t.Errorf("file %s size %d exceeds maxAttachmentBytes %d", f.name, len(f.content), maxAttachmentBytes)
		}
		if f.name == "" {
			t.Errorf("file has empty name")
		}
	}
}

// ---------------------------------------------------------------------------
// Pure helpers
// ---------------------------------------------------------------------------

func TestCodeBlock(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantTruncated bool
		wantNoFences  bool
		wantMaxLen    int
	}{
		{name: "empty input renders fences", input: ""},
		{name: "short input is unchanged inside fences", input: "hello"},
		{
			name:          "long input is truncated",
			input:         strings.Repeat("x", 5000),
			wantTruncated: true,
			wantMaxLen:    inlineContentCap + 128,
		},
		{
			name:         "embedded triple backticks are neutralized",
			input:        "before ``` middle ``` after",
			wantNoFences: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := codeBlock(tt.input)
			if !strings.HasPrefix(out, "```") || !strings.HasSuffix(out, "```") {
				t.Errorf("missing outer fence: %q", out)
			}
			if tt.wantTruncated && !strings.Contains(out, "truncated") {
				t.Errorf("expected truncation marker, got len=%d", len(out))
			}
			if tt.wantMaxLen > 0 && len(out) > tt.wantMaxLen {
				t.Errorf("output longer than expected: %d > %d", len(out), tt.wantMaxLen)
			}
			if tt.wantNoFences {
				inner := out[3 : len(out)-3]
				if strings.Contains(inner, "```") {
					t.Errorf("inner content still contains ```: %q", out)
				}
			}
		})
	}
}

func TestClampBytes(t *testing.T) {
	tests := []struct {
		name       string
		size       int
		max        int
		wantLen    int
		wantMarker bool
	}{
		{name: "under limit unchanged", size: 100, max: 200, wantLen: 100},
		{name: "exactly at limit unchanged", size: 200, max: 200, wantLen: 200},
		{name: "over limit truncated with marker", size: 1000, max: 200, wantLen: 200, wantMarker: true},
		{name: "max smaller than marker still respected", size: 1000, max: 5, wantLen: 5, wantMarker: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := clampBytes([]byte(strings.Repeat("a", tt.size)), tt.max)
			if len(out) > tt.max {
				t.Errorf("len(out)=%d exceeds max %d", len(out), tt.max)
			}
			if len(out) != tt.wantLen {
				t.Errorf("len(out)=%d want %d", len(out), tt.wantLen)
			}
			if tt.wantMarker && !strings.Contains(string(out), "truncated") {
				t.Errorf("expected truncation marker")
			}
		})
	}
}

func TestUserFromMember(t *testing.T) {
	tests := []struct {
		name string
		in   *discordgo.Member
		want *discordgo.User
	}{
		{name: "nil member", in: nil, want: nil},
		{name: "member with user", in: &discordgo.Member{User: &fakeUser}, want: &fakeUser},
		{name: "member with nil user", in: &discordgo.Member{}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := userFromMember(tt.in); got != tt.want {
				t.Errorf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestNamedFile(t *testing.T) {
	a := namedFile("x.txt", "hello")
	if a.name != "x.txt" || a.content != "hello" {
		t.Errorf("unexpected: %+v", a)
	}
}

// ---------------------------------------------------------------------------
// Module gating helpers
// ---------------------------------------------------------------------------

func TestShouldLogChannel(t *testing.T) {
	tests := []struct {
		name      string
		logChan   string
		channelID string
		want      bool
	}{
		{name: "logging disabled (no channel configured)", logChan: "", channelID: "C1", want: false},
		{name: "regular channel logs", logChan: "LOG", channelID: "C1", want: true},
		{name: "the log channel itself is skipped", logChan: "LOG", channelID: "LOG", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.NewMockConfig(map[string]any{
				"gamerpals_1984_log_channel_id": tt.logChan,
			})
			m := New(&types.Dependencies{Config: cfg})
			if got := m.shouldLogChannel(tt.channelID); got != tt.want {
				t.Errorf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestShouldLog(t *testing.T) {
	s := newTestSession()
	tests := []struct {
		name      string
		guildID   string
		channelID string
		author    *discordgo.User
		want      bool
	}{
		{name: "DM (no guild) is skipped", guildID: "", channelID: "C1", author: &fakeUser, want: false},
		{name: "log channel itself is skipped", guildID: testGuildID, channelID: testLogChannelID, author: &fakeUser, want: false},
		{name: "bot author is skipped", guildID: testGuildID, channelID: "C1", author: &discordgo.User{ID: testBotID}, want: false},
		{name: "nil author still logs", guildID: testGuildID, channelID: "C1", author: nil, want: true},
		{name: "regular guild message logs", guildID: testGuildID, channelID: "C1", author: &fakeUser, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := newTestModule(t)
			if got := m.shouldLog(s, tt.guildID, tt.channelID, tt.author); got != tt.want {
				t.Errorf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestChannelInfo(t *testing.T) {
	s := newTestSession(
		textChannel("C1", "general"),
		voiceChannel("V1", "lobby"),
	)
	tests := []struct {
		name        string
		channelID   string
		wantName    string
		wantIsVoice bool
	}{
		{name: "text channel", channelID: "C1", wantName: "general", wantIsVoice: false},
		{name: "voice channel text chat", channelID: "V1", wantName: "lobby", wantIsVoice: true},
		{name: "unknown channel", channelID: "ZZZ", wantName: "(unknown)", wantIsVoice: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := newTestModule(t)
			name, isVoice := m.channelInfo(s, tt.channelID)
			if name != tt.wantName {
				t.Errorf("name=%q want %q", name, tt.wantName)
			}
			if isVoice != tt.wantIsVoice {
				t.Errorf("isVoice=%v want %v", isVoice, tt.wantIsVoice)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Payload builder
// ---------------------------------------------------------------------------

func TestBuildPayload(t *testing.T) {
	longAttachment := strings.Repeat("a", inlineContentCap+1)
	hugeContent := strings.Repeat("y", maxMessageContentChars+500)
	overSizedAttachment := strings.Repeat("Z", 20*1024*1024)
	overflowAttachments := func() []fileAttachment {
		long := strings.Repeat("q", inlineContentCap+10)
		out := make([]fileAttachment, 0, maxAttachmentsPerMessage+5)
		for range maxAttachmentsPerMessage + 5 {
			out = append(out, fileAttachment{name: "a.txt", content: long})
		}
		return out
	}()

	tests := []struct {
		name              string
		content           string
		attachments       []fileAttachment
		wantContentEquals string
		wantContentSubstr string
		wantFileCount     int
		wantFirstFileName string
		wantLastFileName  string
		wantFirstFileEq   string
	}{
		{
			name:              "short content, no attachments",
			content:           "hello world",
			wantContentEquals: "hello world",
		},
		{
			name:        "short attachment is not uploaded",
			content:     "hi",
			attachments: []fileAttachment{{name: "x.txt", content: "tiny"}},
		},
		{
			name:              "empty attachment ignored",
			content:           "hi",
			attachments:       []fileAttachment{{name: "x.txt", content: ""}},
			wantContentEquals: "hi",
		},
		{
			name:              "long attachment is uploaded",
			content:           "header",
			attachments:       []fileAttachment{{name: "content.txt", content: longAttachment}},
			wantFileCount:     1,
			wantFirstFileName: "content.txt",
			wantFirstFileEq:   longAttachment,
		},
		{
			name:              "oversized content is moved to log.txt",
			content:           hugeContent,
			wantFileCount:     1,
			wantFirstFileName: "log.txt",
			wantContentSubstr: "attached",
		},
		{
			name:              "20MB attachment is clamped to maxAttachmentBytes",
			content:           "header",
			attachments:       []fileAttachment{{name: "huge.txt", content: overSizedAttachment}},
			wantFileCount:     1,
			wantFirstFileName: "huge.txt",
		},
		{
			name:              "too many large attachments are dropped with note",
			content:           "header",
			attachments:       overflowAttachments,
			wantFileCount:     maxAttachmentsPerMessage,
			wantContentSubstr: "omitted due to Discord per-message limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := buildPayload(tt.content, tt.attachments, nil)
			assertPayloadWithinLimits(t, p)

			if tt.wantContentEquals != "" && p.content != tt.wantContentEquals {
				t.Errorf("content = %q, want %q", p.content, tt.wantContentEquals)
			}
			if tt.wantContentSubstr != "" && !strings.Contains(p.content, tt.wantContentSubstr) {
				t.Errorf("content %q missing substr %q", p.content, tt.wantContentSubstr)
			}
			if len(p.files) != tt.wantFileCount {
				t.Fatalf("file count = %d, want %d", len(p.files), tt.wantFileCount)
			}
			if tt.wantFirstFileName != "" && p.files[0].name != tt.wantFirstFileName {
				t.Errorf("first file name = %q, want %q", p.files[0].name, tt.wantFirstFileName)
			}
			if tt.wantLastFileName != "" {
				last := p.files[len(p.files)-1]
				if last.name != tt.wantLastFileName {
					t.Errorf("last file name = %q, want %q", last.name, tt.wantLastFileName)
				}
			}
			if tt.wantFirstFileEq != "" && string(p.files[0].content) != tt.wantFirstFileEq {
				t.Errorf("first file content was modified")
			}
		})
	}
}

// TestBuildPayloadStress sweeps a wide combinatorial range of content and
// attachment sizes and counts, asserting that buildPayload always produces
// output within Discord's limits.
func TestBuildPayloadStress(t *testing.T) {
	contentSizes := []int{
		0, 1, 100,
		inlineContentCap - 1, inlineContentCap, inlineContentCap + 1,
		maxMessageContentChars - 1, maxMessageContentChars, maxMessageContentChars + 1,
		10_000, 100_000, 1_000_000,
	}
	attSizes := []int{
		0, 1,
		inlineContentCap, inlineContentCap + 1,
		100_000,
		maxAttachmentBytes - 1, maxAttachmentBytes, maxAttachmentBytes + 1,
		20 * 1024 * 1024,
	}
	attCounts := []int{
		0, 1, 3,
		maxAttachmentsPerMessage,
		maxAttachmentsPerMessage + 1,
		maxAttachmentsPerMessage * 3,
	}

	for _, cSize := range contentSizes {
		for _, aSize := range attSizes {
			for _, n := range attCounts {
				content := strings.Repeat("c", cSize)
				atts := make([]fileAttachment, 0, n)
				for range n {
					atts = append(atts, fileAttachment{
						name:    "f.txt",
						content: strings.Repeat("a", aSize),
					})
				}
				p := buildPayload(content, atts, nil)
				assertPayloadWithinLimits(t, p)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Header rendering
// ---------------------------------------------------------------------------

func TestBuildHeader(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		author      *discordgo.User
		channelID   string
		channelName string
		messageID   string
		isVoiceText bool
		wantSubstrs []string
		notSubstrs  []string
	}{
		{
			name:        "voice text marker is included",
			title:       "X",
			channelID:   "1",
			channelName: "general",
			messageID:   "9",
			isVoiceText: true,
			wantSubstrs: []string{"voice channel text chat", "1", "9"},
		},
		{
			name:        "user, channel, and message ids are included",
			title:       "📝 Message Sent",
			author:      &fakeUser,
			channelID:   "C123",
			channelName: "general",
			messageID:   "M999",
			wantSubstrs: []string{"alice", "U42", "C123", "M999", "general"},
			notSubstrs:  []string{"voice channel text chat"},
		},
		{
			name:        "missing author renders unknown placeholder",
			title:       "🗑️ Message Deleted",
			channelID:   "C7",
			channelName: "lobby",
			messageID:   "M8",
			wantSubstrs: []string{"(unknown)", "C7", "M8"},
		},
		{
			name:        "missing message id is omitted",
			title:       "X",
			author:      &fakeUser,
			channelID:   "C1",
			channelName: "general",
			wantSubstrs: []string{"alice", "C1"},
			notSubstrs:  []string{"Message ID"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := buildHeader(tt.title, tt.author, tt.channelID, tt.channelName, tt.messageID, tt.isVoiceText)
			for _, s := range tt.wantSubstrs {
				if !strings.Contains(out, s) {
					t.Errorf("expected substring %q in header: %q", s, out)
				}
			}
			for _, s := range tt.notSubstrs {
				if strings.Contains(out, s) {
					t.Errorf("did not expect substring %q in header: %q", s, out)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Event handlers (driven through the dispatchLog hook)
// ---------------------------------------------------------------------------

func TestOnMessageCreate(t *testing.T) {
	tests := []struct {
		name        string
		guildID     string
		channelID   string
		author      *discordgo.User
		content     string
		attachments []*discordgo.MessageAttachment
		wantLogged  bool
		wantSubstrs []string
	}{
		{name: "regular message logs content", guildID: testGuildID, channelID: "C1", author: &fakeUser, content: "hello world", wantLogged: true, wantSubstrs: []string{"Message Sent", "alice", "hello world"}},
		{name: "DM is skipped", guildID: "", channelID: "C1", author: &fakeUser, content: "hi", wantLogged: false},
		{name: "log channel itself is skipped", guildID: testGuildID, channelID: testLogChannelID, author: &fakeUser, content: "hi", wantLogged: false},
		{name: "bot's own message is skipped", guildID: testGuildID, channelID: "C1", author: &discordgo.User{ID: testBotID}, content: "hi", wantLogged: false},
		{name: "empty message with no attachments/embeds is skipped", guildID: testGuildID, channelID: "C1", author: &fakeUser, content: "", wantLogged: false},
		{name: "empty content with attachments still logs", guildID: testGuildID, channelID: "C1", author: &fakeUser, content: "", attachments: []*discordgo.MessageAttachment{{Filename: "x.png"}}, wantLogged: true, wantSubstrs: []string{"**Attachments:** 1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, r := newTestModule(t)
			s := newTestSession(textChannel("C1", "general"))
			m.OnMessageCreate(s, &discordgo.MessageCreate{Message: &discordgo.Message{
				ID:          "M1",
				ChannelID:   tt.channelID,
				GuildID:     tt.guildID,
				Author:      tt.author,
				Content:     tt.content,
				Attachments: tt.attachments,
			}})
			if got := len(r.logs) > 0; got != tt.wantLogged {
				t.Fatalf("logged=%v want %v (logs=%d)", got, tt.wantLogged, len(r.logs))
			}
			if !tt.wantLogged {
				return
			}
			assertPayloadWithinLimits(t, r.logs[0].payload)
			if r.logs[0].channelID != testLogChannelID {
				t.Errorf("logged to wrong channel: %s", r.logs[0].channelID)
			}
			for _, sub := range tt.wantSubstrs {
				if !strings.Contains(r.logs[0].payload.content, sub) {
					t.Errorf("missing %q in logged content:\n%s", sub, r.logs[0].payload.content)
				}
			}
		})
	}
}

func TestOnMessageCreate_LongMessageAttachesFile(t *testing.T) {
	m, r := newTestModule(t)
	s := newTestSession(textChannel("C1", "general"))
	long := strings.Repeat("X", inlineContentCap*3)
	m.OnMessageCreate(s, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "M1", ChannelID: "C1", GuildID: testGuildID, Author: &fakeUser, Content: long,
	}})
	if len(r.logs) != 1 {
		t.Fatalf("want 1 log got %d", len(r.logs))
	}
	p := r.logs[0].payload
	assertPayloadWithinLimits(t, p)
	if len(p.files) == 0 {
		t.Fatalf("expected an attachment for long content")
	}
	if !strings.Contains(string(p.files[0].content), "XXXX") {
		t.Errorf("attachment didn't contain original content")
	}
}

func TestOnMessageUpdate(t *testing.T) {
	editTime := time.Now()
	tests := []struct {
		name       string
		before     *discordgo.Message
		guildID    string
		channelID  string
		author     *discordgo.User
		content    string
		edited     *time.Time
		wantLogged bool
		wantSubs   []string
	}{
		{
			name:    "edited message with cached before logs diff",
			before:  &discordgo.Message{Content: "old text", Author: &fakeUser},
			guildID: testGuildID, channelID: "C1", author: &fakeUser, content: "new text",
			edited:     &editTime,
			wantLogged: true,
			wantSubs:   []string{"Edited", "old text", "new text"},
		},
		{
			name:    "no cached before is skipped (cannot verify content changed)",
			before:  nil,
			guildID: testGuildID, channelID: "C1", author: &fakeUser, content: "new text",
			edited:     &editTime,
			wantLogged: false,
		},
		{
			name:    "unchanged content (embed update) is skipped",
			before:  &discordgo.Message{Content: "same", Author: &fakeUser},
			guildID: testGuildID, channelID: "C1", author: &fakeUser, content: "same",
			edited:     &editTime,
			wantLogged: false,
		},
		{
			name:    "nil EditedTimestamp (embed unfurl) is skipped",
			before:  &discordgo.Message{Content: "old", Author: &fakeUser},
			guildID: testGuildID, channelID: "C1", author: &fakeUser, content: "new",
			edited:     nil,
			wantLogged: false,
		},
		{
			name:    "partial payload omitting author falls back to cached author",
			before:  &discordgo.Message{Content: "old", Author: &fakeUser},
			guildID: testGuildID, channelID: "C1", author: nil, content: "new",
			edited:     &editTime,
			wantLogged: true,
			wantSubs:   []string{"Edited", "alice", "old", "new"},
		},
		{
			name:    "no author anywhere is skipped",
			before:  &discordgo.Message{Content: "old"},
			guildID: testGuildID, channelID: "C1", author: nil, content: "new",
			edited:     &editTime,
			wantLogged: false,
		},
		{
			name:    "log channel itself is skipped",
			before:  &discordgo.Message{Content: "x", Author: &fakeUser},
			guildID: testGuildID, channelID: testLogChannelID, author: &fakeUser, content: "y",
			edited:     &editTime,
			wantLogged: false,
		},
		{
			name:    "bot's own edit is skipped",
			before:  &discordgo.Message{Content: "x", Author: &discordgo.User{ID: testBotID}},
			guildID: testGuildID, channelID: "C1", author: &discordgo.User{ID: testBotID}, content: "y",
			edited:     &editTime,
			wantLogged: false,
		},
		{
			name:    "DM edit is skipped",
			before:  &discordgo.Message{Content: "x", Author: &fakeUser},
			guildID: "", channelID: "C1", author: &fakeUser, content: "y",
			edited:     &editTime,
			wantLogged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, r := newTestModule(t)
			s := newTestSession(textChannel("C1", "general"))
			m.OnMessageUpdate(s, &discordgo.MessageUpdate{
				Message: &discordgo.Message{
					ID: "M1", ChannelID: tt.channelID, GuildID: tt.guildID,
					Author: tt.author, Content: tt.content,
					EditedTimestamp: tt.edited,
				},
				BeforeUpdate: tt.before,
			})
			if got := len(r.logs) > 0; got != tt.wantLogged {
				t.Fatalf("logged=%v want %v", got, tt.wantLogged)
			}
			if !tt.wantLogged {
				return
			}
			assertPayloadWithinLimits(t, r.logs[0].payload)
			for _, sub := range tt.wantSubs {
				if !strings.Contains(r.logs[0].payload.content, sub) {
					t.Errorf("missing %q in logged content:\n%s", sub, r.logs[0].payload.content)
				}
			}
		})
	}
}

func TestOnMessageDelete(t *testing.T) {
	tests := []struct {
		name       string
		before     *discordgo.Message
		guildID    string
		channelID  string
		wantLogged bool
		wantSubs   []string
	}{
		{
			name:    "cached delete shows content and author",
			before:  &discordgo.Message{Content: "secret", Author: &fakeUser},
			guildID: testGuildID, channelID: "C1",
			wantLogged: true,
			wantSubs:   []string{"Deleted", "alice", "secret"},
		},
		{
			name:    "uncached delete logs placeholder",
			before:  nil,
			guildID: testGuildID, channelID: "C1",
			wantLogged: true,
			wantSubs:   []string{"Deleted", "(unknown)", "not in cache"},
		},
		{
			name:    "delete with empty content notes no text",
			before:  &discordgo.Message{Content: "", Author: &fakeUser},
			guildID: testGuildID, channelID: "C1",
			wantLogged: true,
			wantSubs:   []string{"no text content"},
		},
		{
			name:    "DM delete is skipped",
			before:  &discordgo.Message{Content: "x", Author: &fakeUser},
			guildID: "", channelID: "C1",
			wantLogged: false,
		},
		{
			name:    "log channel delete is skipped",
			before:  &discordgo.Message{Content: "x", Author: &fakeUser},
			guildID: testGuildID, channelID: testLogChannelID,
			wantLogged: false,
		},
		{
			name:    "bot's own deleted message is skipped",
			before:  &discordgo.Message{Content: "x", Author: &discordgo.User{ID: testBotID}},
			guildID: testGuildID, channelID: "C1",
			wantLogged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, r := newTestModule(t)
			s := newTestSession(textChannel("C1", "general"))
			m.OnMessageDelete(s, &discordgo.MessageDelete{
				Message: &discordgo.Message{
					ID: "M1", ChannelID: tt.channelID, GuildID: tt.guildID,
				},
				BeforeDelete: tt.before,
			})
			if got := len(r.logs) > 0; got != tt.wantLogged {
				t.Fatalf("logged=%v want %v", got, tt.wantLogged)
			}
			if !tt.wantLogged {
				return
			}
			assertPayloadWithinLimits(t, r.logs[0].payload)
			for _, sub := range tt.wantSubs {
				if !strings.Contains(r.logs[0].payload.content, sub) {
					t.Errorf("missing %q in logged content:\n%s", sub, r.logs[0].payload.content)
				}
			}
		})
	}
}

func TestOnMessageReactionAdd_Remove(t *testing.T) {
	tests := []struct {
		name       string
		op         string // "add" or "remove"
		guildID    string
		channelID  string
		userID     string
		emojiName  string
		emojiID    string
		member     *discordgo.Member
		wantLogged bool
		wantSubs   []string
	}{
		{
			name: "add unicode emoji with member",
			op:   "add", guildID: testGuildID, channelID: "C1", userID: "U42",
			emojiName:  "🔥",
			member:     &discordgo.Member{User: &fakeUser},
			wantLogged: true, wantSubs: []string{"Reaction Added", "alice", "🔥"},
		},
		{
			name: "add custom emoji shows id",
			op:   "add", guildID: testGuildID, channelID: "C1", userID: "U42",
			emojiName: "doge", emojiID: "E1",
			member:     &discordgo.Member{User: &fakeUser},
			wantLogged: true, wantSubs: []string{"Reaction Added", "doge", "E1"},
		},
		{
			name: "remove emoji",
			op:   "remove", guildID: testGuildID, channelID: "C1", userID: "U42",
			emojiName:  "👍",
			wantLogged: true, wantSubs: []string{"Reaction Removed", "👍"},
		},
		{
			name: "DM reaction is skipped",
			op:   "add", guildID: "", channelID: "C1", userID: "U42",
			emojiName: "🔥", member: &discordgo.Member{User: &fakeUser},
			wantLogged: false,
		},
		{
			name: "log channel reaction is skipped",
			op:   "add", guildID: testGuildID, channelID: testLogChannelID, userID: "U42",
			emojiName: "🔥", member: &discordgo.Member{User: &fakeUser},
			wantLogged: false,
		},
		{
			name: "bot's own reaction is skipped",
			op:   "add", guildID: testGuildID, channelID: "C1", userID: testBotID,
			emojiName:  "🔥",
			wantLogged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, r := newTestModule(t)
			s := newTestSession(textChannel("C1", "general"))
			mr := &discordgo.MessageReaction{
				UserID:    tt.userID,
				MessageID: "M1",
				ChannelID: tt.channelID,
				GuildID:   tt.guildID,
				Emoji:     discordgo.Emoji{Name: tt.emojiName, ID: tt.emojiID},
			}
			switch tt.op {
			case "add":
				m.OnMessageReactionAdd(s, &discordgo.MessageReactionAdd{MessageReaction: mr, Member: tt.member})
			case "remove":
				m.OnMessageReactionRemove(s, &discordgo.MessageReactionRemove{MessageReaction: mr})
			}
			if got := len(r.logs) > 0; got != tt.wantLogged {
				t.Fatalf("logged=%v want %v", got, tt.wantLogged)
			}
			if !tt.wantLogged {
				return
			}
			assertPayloadWithinLimits(t, r.logs[0].payload)
			for _, sub := range tt.wantSubs {
				if !strings.Contains(r.logs[0].payload.content, sub) {
					t.Errorf("missing %q in logged content:\n%s", sub, r.logs[0].payload.content)
				}
			}
		})
	}
}

func TestSend_NoLogChannelConfiguredIsNoOp(t *testing.T) {
	cfg := config.NewMockConfig(map[string]any{}) // no log channel
	m := New(&types.Dependencies{Config: cfg})
	r := &recorder{}
	m.dispatchLog = r.record
	m.send(nil, "anything")
	if len(r.logs) != 0 {
		t.Errorf("expected no dispatchLog when log channel unset, got %d", len(r.logs))
	}
}

func TestNilEventsAreSafe(t *testing.T) {
	m, r := newTestModule(t)
	s := newTestSession()
	// Each handler should tolerate fully-nil events without panicking or logging.
	m.OnMessageCreate(s, nil)
	m.OnMessageCreate(s, &discordgo.MessageCreate{})
	m.OnMessageUpdate(s, nil)
	m.OnMessageUpdate(s, &discordgo.MessageUpdate{})
	m.OnMessageDelete(s, nil)
	m.OnMessageDelete(s, &discordgo.MessageDelete{})
	m.OnMessageReactionAdd(s, nil)
	m.OnMessageReactionAdd(s, &discordgo.MessageReactionAdd{})
	m.OnMessageReactionRemove(s, nil)
	m.OnMessageReactionRemove(s, &discordgo.MessageReactionRemove{})
	if len(r.logs) != 0 {
		t.Errorf("expected no logs from nil/empty events, got %d", len(r.logs))
	}
}

// ---------------------------------------------------------------------------
// Module wiring
// ---------------------------------------------------------------------------

func TestModuleService_IsNil(t *testing.T) {
	m, _ := newTestModule(t)
	if m.Service() != nil {
		t.Errorf("Service should be nil")
	}
}

func TestModuleRegister_RegistersNoCommands(t *testing.T) {
	m, _ := newTestModule(t)
	cmds := map[string]*types.Command{}
	m.Register(cmds, &types.Dependencies{Config: m.config})
	if len(cmds) != 0 {
		t.Errorf("expected zero commands registered, got %d", len(cmds))
	}
	if m.dispatchLog == nil {
		t.Errorf("Register must leave dispatchLog installed")
	}
}

// ---------------------------------------------------------------------------
// Channel-level handlers (creation, rename, status update)
// ---------------------------------------------------------------------------

func TestOnChannelCreate(t *testing.T) {
	tests := []struct {
		name        string
		channel     *discordgo.Channel
		guildID     string
		wantLogged  bool
		wantSubstrs []string
	}{
		{
			name:        "voice channel logs",
			channel:     &discordgo.Channel{ID: "VC1", Name: "general-voice", Type: discordgo.ChannelTypeGuildVoice},
			guildID:     testGuildID,
			wantLogged:  true,
			wantSubstrs: []string{"Voice Channel Created", "general-voice", "VC1", "[voice]"},
		},
		{
			name:        "stage voice channel logs",
			channel:     &discordgo.Channel{ID: "ST1", Name: "main-stage", Type: discordgo.ChannelTypeGuildStageVoice},
			guildID:     testGuildID,
			wantLogged:  true,
			wantSubstrs: []string{"main-stage", "[stage voice]"},
		},
		{
			name:       "text channel is skipped",
			channel:    &discordgo.Channel{ID: "C1", Name: "general", Type: discordgo.ChannelTypeGuildText},
			guildID:    testGuildID,
			wantLogged: false,
		},
		{
			name:       "DM channel is skipped",
			channel:    &discordgo.Channel{ID: "VC1", Name: "x", Type: discordgo.ChannelTypeGuildVoice},
			guildID:    "",
			wantLogged: false,
		},
		{
			name:       "log channel itself is skipped",
			channel:    &discordgo.Channel{ID: testLogChannelID, Name: "logs", Type: discordgo.ChannelTypeGuildVoice},
			guildID:    testGuildID,
			wantLogged: false,
		},
		{
			name:       "nil channel is skipped",
			channel:    nil,
			guildID:    testGuildID,
			wantLogged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, r := newTestModule(t)
			s := newTestSession()
			e := &discordgo.ChannelCreate{Channel: tt.channel}
			if tt.channel != nil {
				tt.channel.GuildID = tt.guildID
			}
			m.OnChannelCreate(s, e)
			if got := len(r.logs) > 0; got != tt.wantLogged {
				t.Fatalf("logged=%v want %v (logs=%d)", got, tt.wantLogged, len(r.logs))
			}
			if !tt.wantLogged {
				return
			}
			assertPayloadWithinLimits(t, r.logs[0].payload)
			for _, sub := range tt.wantSubstrs {
				if !strings.Contains(r.logs[0].payload.content, sub) {
					t.Errorf("missing %q in:\n%s", sub, r.logs[0].payload.content)
				}
			}
		})
	}
}

func TestOnChannelCreate_NilEvent(t *testing.T) {
	m, r := newTestModule(t)
	s := newTestSession()
	m.OnChannelCreate(s, nil)
	if len(r.logs) != 0 {
		t.Fatalf("expected no log on nil event, got %d", len(r.logs))
	}
}

func TestOnChannelUpdate(t *testing.T) {
	voiceCh := func(id, name string) *discordgo.Channel {
		return &discordgo.Channel{ID: id, Name: name, Type: discordgo.ChannelTypeGuildVoice, GuildID: testGuildID}
	}
	tests := []struct {
		name        string
		before      *discordgo.Channel
		after       *discordgo.Channel
		wantLogged  bool
		wantSubstrs []string
	}{
		{
			name:        "rename logs before/after",
			before:      voiceCh("VC1", "old-name"),
			after:       voiceCh("VC1", "new-name"),
			wantLogged:  true,
			wantSubstrs: []string{"Voice Channel Renamed", "old-name", "new-name"},
		},
		{
			name:       "uncached update is skipped (cannot confirm rename)",
			before:     nil,
			after:      voiceCh("VC1", "new-name"),
			wantLogged: false,
		},
		{
			name:       "no name change is skipped",
			before:     voiceCh("VC1", "same"),
			after:      voiceCh("VC1", "same"),
			wantLogged: false,
		},
		{
			name:       "text channel is skipped",
			before:     &discordgo.Channel{ID: "C1", Name: "old", Type: discordgo.ChannelTypeGuildText, GuildID: testGuildID},
			after:      &discordgo.Channel{ID: "C1", Name: "new", Type: discordgo.ChannelTypeGuildText, GuildID: testGuildID},
			wantLogged: false,
		},
		{
			name:       "DM is skipped",
			before:     voiceCh("VC1", "old"),
			after:      &discordgo.Channel{ID: "VC1", Name: "new", Type: discordgo.ChannelTypeGuildVoice},
			wantLogged: false,
		},
		{
			name:       "log channel itself is skipped",
			before:     voiceCh(testLogChannelID, "old"),
			after:      voiceCh(testLogChannelID, "new"),
			wantLogged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, r := newTestModule(t)
			s := newTestSession()
			e := &discordgo.ChannelUpdate{Channel: tt.after, BeforeUpdate: tt.before}
			m.OnChannelUpdate(s, e)
			if got := len(r.logs) > 0; got != tt.wantLogged {
				t.Fatalf("logged=%v want %v (logs=%d)", got, tt.wantLogged, len(r.logs))
			}
			if !tt.wantLogged {
				return
			}
			assertPayloadWithinLimits(t, r.logs[0].payload)
			for _, sub := range tt.wantSubstrs {
				if !strings.Contains(r.logs[0].payload.content, sub) {
					t.Errorf("missing %q in:\n%s", sub, r.logs[0].payload.content)
				}
			}
		})
	}
}

func TestOnChannelUpdate_NilEvent(t *testing.T) {
	m, r := newTestModule(t)
	s := newTestSession()
	m.OnChannelUpdate(s, nil)
	if len(r.logs) != 0 {
		t.Fatalf("expected no log on nil, got %d", len(r.logs))
	}
}

func TestOnRawEvent_VoiceChannelStatusUpdate(t *testing.T) {
	tests := []struct {
		name        string
		eventType   string
		rawData     string
		wantLogged  bool
		wantSubstrs []string
	}{
		{
			name:        "status set logs new value",
			eventType:   voiceChannelStatusUpdateRawType,
			rawData:     `{"id":"VC1","guild_id":"G1","status":"playing minecraft"}`,
			wantLogged:  true,
			wantSubstrs: []string{"Voice Channel Status Updated", "VC1", "playing minecraft", "[voice]"},
		},
		{
			name:        "empty status logs cleared",
			eventType:   voiceChannelStatusUpdateRawType,
			rawData:     `{"id":"VC1","guild_id":"G1","status":""}`,
			wantLogged:  true,
			wantSubstrs: []string{"(cleared)"},
		},
		{
			name:       "missing guild_id is skipped",
			eventType:  voiceChannelStatusUpdateRawType,
			rawData:    `{"id":"VC1","status":"hi"}`,
			wantLogged: false,
		},
		{
			name:       "log channel itself is skipped",
			eventType:  voiceChannelStatusUpdateRawType,
			rawData:    `{"id":"` + testLogChannelID + `","guild_id":"G1","status":"hi"}`,
			wantLogged: false,
		},
		{
			name:       "non-status event types are ignored",
			eventType:  "MESSAGE_CREATE",
			rawData:    `{"id":"VC1","guild_id":"G1","status":"hi"}`,
			wantLogged: false,
		},
		{
			name:       "malformed JSON is ignored",
			eventType:  voiceChannelStatusUpdateRawType,
			rawData:    `{not json`,
			wantLogged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, r := newTestModule(t)
			s := newTestSession(voiceChannel("VC1", "general-voice"))
			e := &discordgo.Event{Type: tt.eventType, RawData: []byte(tt.rawData)}
			m.OnRawEvent(s, e)
			if got := len(r.logs) > 0; got != tt.wantLogged {
				t.Fatalf("logged=%v want %v (logs=%d)", got, tt.wantLogged, len(r.logs))
			}
			if !tt.wantLogged {
				return
			}
			assertPayloadWithinLimits(t, r.logs[0].payload)
			for _, sub := range tt.wantSubstrs {
				if !strings.Contains(r.logs[0].payload.content, sub) {
					t.Errorf("missing %q in:\n%s", sub, r.logs[0].payload.content)
				}
			}
		})
	}
}

func TestOnRawEvent_NilOrEmpty(t *testing.T) {
	m, r := newTestModule(t)
	s := newTestSession()
	m.OnRawEvent(s, nil)
	if len(r.logs) != 0 {
		t.Fatalf("expected no log on nil event, got %d", len(r.logs))
	}
}

func TestChannelTypeName(t *testing.T) {
	tests := []struct {
		name string
		in   discordgo.ChannelType
		want string
	}{
		{name: "voice", in: discordgo.ChannelTypeGuildVoice, want: "voice"},
		{name: "stage voice", in: discordgo.ChannelTypeGuildStageVoice, want: "stage voice"},
		{name: "text falls back to channel", in: discordgo.ChannelTypeGuildText, want: "channel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := channelTypeName(tt.in); got != tt.want {
				t.Errorf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestIsVoiceLike(t *testing.T) {
	tests := []struct {
		name string
		in   discordgo.ChannelType
		want bool
	}{
		{name: "voice", in: discordgo.ChannelTypeGuildVoice, want: true},
		{name: "stage", in: discordgo.ChannelTypeGuildStageVoice, want: true},
		{name: "text", in: discordgo.ChannelTypeGuildText, want: false},
		{name: "category", in: discordgo.ChannelTypeGuildCategory, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isVoiceLike(tt.in); got != tt.want {
				t.Errorf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestUTF8TruncationDoesNotSplitRunes(t *testing.T) {
	// Build a string whose byte boundary at inlineContentCap lands inside a
	// multi-byte rune. 😀 is 4 bytes; pad with ASCII so the cut sits inside it.
	tests := []struct {
		name string
		fn   func() string
	}{
		{
			name: "codeBlock truncation respects rune boundaries",
			fn: func() string {
				body := strings.Repeat("a", inlineContentCap-1) + "😀😀😀"
				return codeBlock(body)
			},
		},
		{
			name: "clampBytes truncation respects rune boundaries (with marker)",
			fn: func() string {
				body := strings.Repeat("a", maxAttachmentBytes-1) + "😀😀😀"
				return string(clampBytes([]byte(body), maxAttachmentBytes))
			},
		},
		{
			name: "clampBytes truncation respects rune boundaries (under marker size)",
			fn: func() string {
				// max smaller than the marker; ASCII pad then a multi-byte rune.
				body := strings.Repeat("a", 5) + "😀"
				return string(clampBytes([]byte(body), 7))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn()
			if !utf8.ValidString(got) {
				t.Errorf("output is not valid UTF-8")
			}
		})
	}
}

func TestRenderUnifiedDiff(t *testing.T) {
	tests := []struct {
		name        string
		before      string
		after       string
		wantSubstrs []string
		wantEmpty   bool
	}{
		{
			name:        "single line change shows minus and plus",
			before:      "hello",
			after:       "world",
			wantSubstrs: []string{"-hello", "+world", "@@"},
		},
		{
			name:        "added line",
			before:      "a\n",
			after:       "a\nb\n",
			wantSubstrs: []string{"+b"},
		},
		{
			name:      "identical inputs produce empty diff",
			before:    "same",
			after:     "same",
			wantEmpty: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderUnifiedDiff(tt.before, tt.after)
			if tt.wantEmpty {
				if got != "" {
					t.Errorf("expected empty diff, got %q", got)
				}
				return
			}
			for _, sub := range tt.wantSubstrs {
				if !strings.Contains(got, sub) {
					t.Errorf("missing %q in diff:\n%s", sub, got)
				}
			}
		})
	}
}

func TestOnMessageUpdate_RendersDiffBlock(t *testing.T) {
	m, r := newTestModule(t)
	s := newTestSession(textChannel("C1", "general"))
	editTime := time.Now()
	m.OnMessageUpdate(s, &discordgo.MessageUpdate{
		Message: &discordgo.Message{
			ID: "M1", ChannelID: "C1", GuildID: testGuildID,
			Author: &fakeUser, Content: "new text", EditedTimestamp: &editTime,
		},
		BeforeUpdate: &discordgo.Message{Content: "old text", Author: &fakeUser},
	})
	if len(r.logs) != 1 {
		t.Fatalf("want 1 log got %d", len(r.logs))
	}
	p := r.logs[0].payload
	assertPayloadWithinLimits(t, p)
	if !strings.Contains(p.content, "```diff") {
		t.Errorf("expected ```diff fenced block, got:\n%s", p.content)
	}
	if !strings.Contains(p.content, "-old text") || !strings.Contains(p.content, "+new text") {
		t.Errorf("expected diff lines, got:\n%s", p.content)
	}
}

func TestOnMessageUpdate_LongDiffStaysWithinLimits(t *testing.T) {
	// Build large before/after that produce a diff well over inline limits.
	// Each line is ~80 chars; 200 lines ~= 16KB before, with every line
	// changed in after, producing ~32KB of diff hunk lines.
	var beforeBuf, afterBuf strings.Builder
	for range 200 {
		beforeBuf.WriteString(strings.Repeat("a", 80))
		beforeBuf.WriteByte('\n')
		afterBuf.WriteString(strings.Repeat("b", 80))
		afterBuf.WriteByte('\n')
	}

	m, r := newTestModule(t)
	s := newTestSession(textChannel("C1", "general"))
	editTime := time.Now()
	m.OnMessageUpdate(s, &discordgo.MessageUpdate{
		Message: &discordgo.Message{
			ID: "M1", ChannelID: "C1", GuildID: testGuildID,
			Author: &fakeUser, Content: afterBuf.String(), EditedTimestamp: &editTime,
		},
		BeforeUpdate: &discordgo.Message{Content: beforeBuf.String(), Author: &fakeUser},
	})

	if len(r.logs) != 1 {
		t.Fatalf("want 1 log got %d", len(r.logs))
	}
	assertPayloadWithinLimits(t, r.logs[0].payload)
}

func TestCodeBlockLang(t *testing.T) {
	tests := []struct {
		name        string
		lang        string
		input       string
		wantPrefix  string
		wantContent string
	}{
		{name: "diff lang", lang: "diff", input: "-a\n+b", wantPrefix: "```diff\n"},
		{name: "no lang", lang: "", input: "x", wantPrefix: "```\n"},
		{name: "empty body diff", lang: "diff", input: "", wantContent: "```diff\n```"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := codeBlockLang(tt.lang, tt.input)
			if tt.wantContent != "" {
				if got != tt.wantContent {
					t.Errorf("got %q want %q", got, tt.wantContent)
				}
				return
			}
			if !strings.HasPrefix(got, tt.wantPrefix) {
				t.Errorf("missing prefix %q in:\n%s", tt.wantPrefix, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Image attachment re-hosting
// ---------------------------------------------------------------------------

func TestIsImageAttachment(t *testing.T) {
	tests := []struct {
		name string
		att  *discordgo.MessageAttachment
		want bool
	}{
		{"image content type", &discordgo.MessageAttachment{ContentType: "image/png", Filename: "x"}, true},
		{"image content type uppercase", &discordgo.MessageAttachment{ContentType: "IMAGE/JPEG", Filename: "x"}, true},
		{"png by ext", &discordgo.MessageAttachment{Filename: "pic.PNG"}, true},
		{"jpg by ext", &discordgo.MessageAttachment{Filename: "pic.jpg"}, true},
		{"webp by ext", &discordgo.MessageAttachment{Filename: "pic.webp"}, true},
		{"gif by ext", &discordgo.MessageAttachment{Filename: "anim.gif"}, true},
		{"non-image text", &discordgo.MessageAttachment{ContentType: "text/plain", Filename: "notes.txt"}, false},
		{"non-image video", &discordgo.MessageAttachment{ContentType: "video/mp4", Filename: "clip.mp4"}, false},
		{"unknown ext", &discordgo.MessageAttachment{Filename: "data.bin"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isImageAttachment(tt.att); got != tt.want {
				t.Errorf("isImageAttachment(%+v) = %v, want %v", tt.att, got, tt.want)
			}
		})
	}
}

func TestRehostImages(t *testing.T) {
	pngBytes := []byte("\x89PNG\r\n\x1a\nfakeimagedata")
	tests := []struct {
		name        string
		attachments []*discordgo.MessageAttachment
		fetcher     func(url string, max int) ([]byte, error)
		wantFiles   int
		wantNotes   int
		wantNoteSub string
	}{
		{
			name:        "no attachments",
			attachments: nil,
			wantFiles:   0,
			wantNotes:   0,
		},
		{
			name: "single image rehosted",
			attachments: []*discordgo.MessageAttachment{
				{Filename: "pic.png", ContentType: "image/png", URL: "https://x/pic.png", Size: 100},
			},
			fetcher:   func(_ string, _ int) ([]byte, error) { return pngBytes, nil },
			wantFiles: 1,
			wantNotes: 0,
		},
		{
			name: "non-image skipped silently",
			attachments: []*discordgo.MessageAttachment{
				{Filename: "doc.pdf", ContentType: "application/pdf", URL: "https://x/doc.pdf", Size: 100},
			},
			fetcher:   func(_ string, _ int) ([]byte, error) { return pngBytes, nil },
			wantFiles: 0,
			wantNotes: 0,
		},
		{
			name: "fetch failure produces note, no file",
			attachments: []*discordgo.MessageAttachment{
				{Filename: "pic.png", ContentType: "image/png", URL: "https://x/pic.png", Size: 100},
			},
			fetcher:     func(_ string, _ int) ([]byte, error) { return nil, errFetch },
			wantFiles:   0,
			wantNotes:   1,
			wantNoteSub: "not re-hosted",
		},
		{
			name: "oversized declared size produces note, fetcher not called",
			attachments: []*discordgo.MessageAttachment{
				{Filename: "huge.png", ContentType: "image/png", URL: "https://x/huge.png", Size: maxAttachmentBytes + 1},
			},
			fetcher: func(_ string, _ int) ([]byte, error) {
				t.Fatalf("fetcher should not be called for oversized attachment")
				return nil, nil
			},
			wantFiles:   0,
			wantNotes:   1,
			wantNoteSub: "exceeds",
		},
		{
			name: "mixed: image rehosted, text skipped, broken-image noted",
			attachments: []*discordgo.MessageAttachment{
				{Filename: "ok.png", ContentType: "image/png", URL: "https://x/ok.png", Size: 100},
				{Filename: "notes.txt", ContentType: "text/plain", URL: "https://x/notes.txt", Size: 100},
				{Filename: "broken.jpg", ContentType: "image/jpeg", URL: "https://x/broken.jpg", Size: 100},
			},
			fetcher: func(url string, _ int) ([]byte, error) {
				if strings.Contains(url, "broken") {
					return nil, errFetch
				}
				return pngBytes, nil
			},
			wantFiles: 1,
			wantNotes: 1,
		},
		{
			name: "nil entry tolerated",
			attachments: []*discordgo.MessageAttachment{
				nil,
				{Filename: "pic.png", ContentType: "image/png", URL: "https://x/pic.png", Size: 100},
			},
			fetcher:   func(_ string, _ int) ([]byte, error) { return pngBytes, nil },
			wantFiles: 1,
			wantNotes: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := newTestModule(t)
			if tt.fetcher != nil {
				m.fetchImage = tt.fetcher
			}
			files, notes := m.rehostImages(tt.attachments)
			if len(files) != tt.wantFiles {
				t.Errorf("files = %d, want %d", len(files), tt.wantFiles)
			}
			if len(notes) != tt.wantNotes {
				t.Errorf("notes = %d, want %d (notes=%v)", len(notes), tt.wantNotes, notes)
			}
			if tt.wantNoteSub != "" {
				found := false
				for _, n := range notes {
					if strings.Contains(n, tt.wantNoteSub) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("no note contained %q; notes=%v", tt.wantNoteSub, notes)
				}
			}
			for _, f := range files {
				if f.name == "" {
					t.Error("rehosted file has empty name")
				}
				if len(f.content) == 0 {
					t.Error("rehosted file has empty content")
				}
			}
		})
	}
}

var errFetch = fakeErr("fetch failed")

type fakeErr string

func (e fakeErr) Error() string { return string(e) }

func TestOnMessageCreate_RehostsImages(t *testing.T) {
	m, r := newTestModule(t)
	pngBytes := []byte("\x89PNG\r\n\x1a\nfakeimagedata")
	m.fetchImage = func(url string, _ int) ([]byte, error) {
		return pngBytes, nil
	}
	s := newTestSession(textChannel("C1", "general"))

	m.OnMessageCreate(s, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "M1", ChannelID: "C1", GuildID: testGuildID, Author: &fakeUser,
		Content: "hello",
		Attachments: []*discordgo.MessageAttachment{
			{Filename: "pic.png", ContentType: "image/png", URL: "https://x/pic.png", Size: 100},
		},
	}})

	if len(r.logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(r.logs))
	}
	p := r.logs[0].payload
	assertPayloadWithinLimits(t, p)
	if len(p.files) != 1 {
		t.Fatalf("files = %d, want 1", len(p.files))
	}
	if p.files[0].name != "pic.png" {
		t.Errorf("file name = %q, want pic.png", p.files[0].name)
	}
	if string(p.files[0].content) != string(pngBytes) {
		t.Errorf("file content not preserved")
	}
}

func TestOnMessageCreate_SkipsNonImageAttachments(t *testing.T) {
	m, r := newTestModule(t)
	called := 0
	m.fetchImage = func(_ string, _ int) ([]byte, error) {
		called++
		return []byte("data"), nil
	}
	s := newTestSession(textChannel("C1", "general"))

	m.OnMessageCreate(s, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "M1", ChannelID: "C1", GuildID: testGuildID, Author: &fakeUser,
		Content: "hello",
		Attachments: []*discordgo.MessageAttachment{
			{Filename: "doc.pdf", ContentType: "application/pdf", URL: "https://x/doc.pdf", Size: 100},
			{Filename: "clip.mp4", ContentType: "video/mp4", URL: "https://x/clip.mp4", Size: 100},
		},
	}})

	if called != 0 {
		t.Errorf("fetcher called %d times for non-image attachments, want 0", called)
	}
	if len(r.logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(r.logs))
	}
	if len(r.logs[0].payload.files) != 0 {
		t.Errorf("files = %d, want 0", len(r.logs[0].payload.files))
	}
}

func TestOnMessageCreate_FetchFailureStillLogs(t *testing.T) {
	m, r := newTestModule(t)
	m.fetchImage = func(_ string, _ int) ([]byte, error) {
		return nil, errFetch
	}
	s := newTestSession(textChannel("C1", "general"))

	m.OnMessageCreate(s, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "M1", ChannelID: "C1", GuildID: testGuildID, Author: &fakeUser,
		Content: "hi",
		Attachments: []*discordgo.MessageAttachment{
			{Filename: "pic.png", ContentType: "image/png", URL: "https://x/pic.png", Size: 100},
		},
	}})

	if len(r.logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(r.logs))
	}
	if !strings.Contains(r.logs[0].payload.content, "not re-hosted") {
		t.Errorf("expected failure note in content, got: %q", r.logs[0].payload.content)
	}
	if len(r.logs[0].payload.files) != 0 {
		t.Errorf("files = %d, want 0 on fetch failure", len(r.logs[0].payload.files))
	}
}

func TestBuildPayload_ExtraFilesPriority(t *testing.T) {
	// With 11 binary files plus content > inline cap (which would normally
	// add a log.txt), we should drop overflow and note it. Binary files
	// should keep their slots; the long content text fallback is the one
	// that gets dropped.
	bins := make([]fileSpec, maxAttachmentsPerMessage+1)
	for i := range bins {
		bins[i] = fileSpec{name: "img.png", content: []byte("img")}
	}
	p := buildPayload("short", nil, bins)
	assertPayloadWithinLimits(t, p)
	if len(p.files) != maxAttachmentsPerMessage {
		t.Fatalf("files = %d, want %d", len(p.files), maxAttachmentsPerMessage)
	}
	if !strings.Contains(p.content, "omitted") {
		t.Errorf("expected omitted note in content, got: %q", p.content)
	}
}

func TestDetectContentType(t *testing.T) {
	tests := []struct {
		name    string
		fname   string
		data    []byte
		wantSub string
	}{
		{"txt", "log.txt", []byte("hello"), "text/plain"},
		{"patch", "diff.patch", []byte("--- a"), "text/plain"},
		{"png", "pic.png", []byte("\x89PNG\r\n\x1a\n"), "image/png"},
		{"empty", "x.bin", nil, "application/octet-stream"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectContentType(tt.fname, tt.data)
			if !strings.Contains(got, tt.wantSub) {
				t.Errorf("detectContentType(%q) = %q, want substr %q", tt.fname, got, tt.wantSub)
			}
		})
	}
}
