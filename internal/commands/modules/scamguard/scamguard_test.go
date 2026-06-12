package scamguard

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/require"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/database"
)

// ---------------------------------------------------------------------------
// Fixture images (generated in-code so tests need no binary assets)
// ---------------------------------------------------------------------------

func makeGradient(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8((x * 255) / w)
			img.Set(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	return img
}

func makeChecker(w, h, block int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			on := ((x/block)+(y/block))%2 == 0
			c := color.RGBA{A: 255}
			if on {
				c.R, c.G, c.B = 255, 255, 255
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, img image.Image, q int) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}))
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// Test harness
// ---------------------------------------------------------------------------

type timeoutCall struct {
	guildID string
	userID  string
	until   *time.Time
}

type enforceRec struct {
	mu       sync.Mutex
	deleted  []string
	timeouts []timeoutCall
	logs     []*discordgo.MessageEmbed
	isMod    bool
}

func (r *enforceRec) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted = nil
	r.timeouts = nil
	r.logs = nil
}

// newTestModule builds a module with recording seams and an injectable image
// store. db is nil so the hash cache lives purely in memory.
func newTestModule(t *testing.T, kv map[string]interface{}) (*Module, *enforceRec, map[string][]byte) {
	t.Helper()
	cfg := config.NewMockConfig(kv)
	m := New(&types.Dependencies{Config: cfg})

	rec := &enforceRec{}
	images := map[string][]byte{}

	m.fetchImage = func(url string, _ int) ([]byte, error) {
		if b, ok := images[url]; ok {
			return b, nil
		}
		return nil, image.ErrFormat
	}
	m.deleteMessage = func(_ *discordgo.Session, _, messageID string) error {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.deleted = append(rec.deleted, messageID)
		return nil
	}
	m.timeoutMember = func(_ *discordgo.Session, guildID, userID string, until *time.Time) error {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.timeouts = append(rec.timeouts, timeoutCall{guildID: guildID, userID: userID, until: until})
		return nil
	}
	m.sendLogEmbed = func(_ *discordgo.Session, _ string, embed *discordgo.MessageEmbed) error {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.logs = append(rec.logs, embed)
		return nil
	}
	m.authorIsModerator = func(_ *discordgo.Session, _ *discordgo.MessageCreate) bool {
		return rec.isMod
	}
	return m, rec, images
}

func imageMessage(url, filename, contentType string, size int) *discordgo.MessageCreate {
	return &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "M1",
		ChannelID: "C1",
		GuildID:   "G1",
		Author:    &discordgo.User{ID: "U1", Username: "spammer"},
		Attachments: []*discordgo.MessageAttachment{{
			URL:         url,
			Filename:    filename,
			ContentType: contentType,
			Size:        size,
		}},
	}}
}

const enabledLog = "LOGCH"

func enabledKV(action string) map[string]interface{} {
	return map[string]interface{}{
		"scamguard_enabled":        true,
		"scamguard_action":         action,
		"scamguard_log_channel_id": enabledLog,
	}
}

// ---------------------------------------------------------------------------
// Hashing
// ---------------------------------------------------------------------------

func TestComputeHash_StableAcrossReencodeAndResize(t *testing.T) {
	base := makeGradient(256, 256)
	hBase, err := computeHash(encodePNG(t, base))
	require.NoError(t, err)

	// Same picture, lossy JPEG re-encode.
	hJPEG, err := computeHash(encodeJPEG(t, base, 80))
	require.NoError(t, err)
	dJPEG, err := hBase.Distance(hJPEG)
	require.NoError(t, err)
	require.LessOrEqual(t, dJPEG, 8, "re-encoded same image should stay within threshold")

	// Same picture at a different resolution.
	hResized, err := computeHash(encodePNG(t, makeGradient(200, 180)))
	require.NoError(t, err)
	dResized, err := hBase.Distance(hResized)
	require.NoError(t, err)
	require.LessOrEqual(t, dResized, 8, "resized same image should stay within threshold")
}

func TestComputeHash_DifferentImagesAreFar(t *testing.T) {
	hGrad, err := computeHash(encodePNG(t, makeGradient(256, 256)))
	require.NoError(t, err)
	hCheck, err := computeHash(encodePNG(t, makeChecker(256, 256, 8)))
	require.NoError(t, err)
	d, err := hGrad.Distance(hCheck)
	require.NoError(t, err)
	require.Greater(t, d, 8, "structurally different images should exceed threshold")
}

func TestComputeHash_DecodeError(t *testing.T) {
	_, err := computeHash([]byte("not an image"))
	require.Error(t, err)
}

// craftPNGHeader builds a valid PNG signature + IHDR chunk declaring the given
// dimensions, with no pixel data. image.DecodeConfig reads it (and the CRC must
// be correct), but a full image.Decode never happens, so it stays tiny on disk
// while declaring an arbitrarily huge bitmap.
func craftPNGHeader(t *testing.T, width, height uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) // PNG signature

	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:4], width)
	binary.BigEndian.PutUint32(data[4:8], height)
	data[8] = 8 // bit depth
	data[9] = 2 // color type: truecolor (RGB)
	// data[10..12] (compression, filter, interlace) stay 0.

	var lenb [4]byte
	binary.BigEndian.PutUint32(lenb[:], uint32(len(data)))
	buf.Write(lenb[:])

	typ := []byte("IHDR")
	buf.Write(typ)
	buf.Write(data)

	crc := crc32.NewIEEE()
	_, _ = crc.Write(typ)
	_, _ = crc.Write(data)
	var crcb [4]byte
	binary.BigEndian.PutUint32(crcb[:], crc.Sum32())
	buf.Write(crcb[:])

	return buf.Bytes()
}

func TestComputeHash_RejectsOversizedImage(t *testing.T) {
	// A header-only PNG declaring enormous dimensions: a few dozen bytes on disk,
	// but a full decode would allocate a multi-gigabyte bitmap. computeHash must
	// reject it via the dimension guard before attempting the full decode.
	header := craftPNGHeader(t, 100000, 100000)

	cfg, _, err := image.DecodeConfig(bytes.NewReader(header))
	require.NoError(t, err) // sanity: the crafted header itself is valid
	require.Equal(t, 100000, cfg.Width)
	require.Equal(t, 100000, cfg.Height)

	_, err = computeHash(header)
	require.Error(t, err)
	require.Contains(t, err.Error(), "too large")
}

// ---------------------------------------------------------------------------
// Matching / store
// ---------------------------------------------------------------------------

func TestMatchHash(t *testing.T) {
	m, _, _ := newTestModule(t, nil)
	seed, err := computeHash(encodePNG(t, makeGradient(256, 256)))
	require.NoError(t, err)
	added, err := m.addKnownHash(hashString(seed), "test", "seed")
	require.NoError(t, err)
	require.True(t, added)
	require.Equal(t, 1, m.hashCount())

	// Same image, re-encoded -> match.
	incoming, err := computeHash(encodeJPEG(t, makeGradient(256, 256), 80))
	require.NoError(t, err)
	_, ok := m.matchHash(incoming, 8)
	require.True(t, ok)

	// Different image -> no match.
	other, err := computeHash(encodePNG(t, makeChecker(256, 256, 8)))
	require.NoError(t, err)
	_, ok = m.matchHash(other, 8)
	require.False(t, ok)
}

func TestAddKnownHash_Dedupes(t *testing.T) {
	m, _, _ := newTestModule(t, nil)
	h, err := computeHash(encodePNG(t, makeGradient(64, 64)))
	require.NoError(t, err)
	s := hashString(h)

	added, err := m.addKnownHash(s, "test", "seed")
	require.NoError(t, err)
	require.True(t, added)

	added, err = m.addKnownHash(s, "test", "seed")
	require.NoError(t, err)
	require.False(t, added)
	require.Equal(t, 1, m.hashCount())
}

func TestParseSeedLines(t *testing.T) {
	in := "# comment\n\n  p:ff00  \n p:1234\n# trailing\n"
	got := parseSeedLines(in)
	require.Equal(t, []string{"p:ff00", "p:1234"}, got)
}

func TestIsImageAttachment(t *testing.T) {
	require.True(t, isImageAttachment(&discordgo.MessageAttachment{ContentType: "image/png"}))
	require.True(t, isImageAttachment(&discordgo.MessageAttachment{Filename: "scam.JPG"}))
	require.True(t, isImageAttachment(&discordgo.MessageAttachment{Filename: "a.webp"}))
	require.False(t, isImageAttachment(&discordgo.MessageAttachment{Filename: "notes.txt", ContentType: "text/plain"}))
}

// ---------------------------------------------------------------------------
// OnMessageCreate
// ---------------------------------------------------------------------------

func seedGradient(t *testing.T, m *Module) {
	t.Helper()
	h, err := computeHash(encodePNG(t, makeGradient(256, 256)))
	require.NoError(t, err)
	_, err = m.addKnownHash(hashString(h), "test", "seed")
	require.NoError(t, err)
}

func TestOnMessageCreate_MatchTimesOut(t *testing.T) {
	m, rec, images := newTestModule(t, enabledKV("timeout"))
	seedGradient(t, m)
	images["grad"] = encodeJPEG(t, makeGradient(256, 256), 80)

	e := imageMessage("grad", "scam.jpg", "image/jpeg", len(images["grad"]))
	m.OnMessageCreate(nil, e)

	require.Equal(t, []string{"M1"}, rec.deleted)
	require.Len(t, rec.timeouts, 1)
	require.Equal(t, "U1", rec.timeouts[0].userID)
	require.Equal(t, "G1", rec.timeouts[0].guildID)
	require.NotNil(t, rec.timeouts[0].until)
	require.True(t, rec.timeouts[0].until.After(time.Now()))
	require.Len(t, rec.logs, 1)
}

func TestOnMessageCreate_ActionDelete_NoTimeout(t *testing.T) {
	m, rec, images := newTestModule(t, enabledKV("delete"))
	seedGradient(t, m)
	images["grad"] = encodePNG(t, makeGradient(256, 256))

	m.OnMessageCreate(nil, imageMessage("grad", "scam.png", "image/png", len(images["grad"])))

	require.Equal(t, []string{"M1"}, rec.deleted)
	require.Empty(t, rec.timeouts)
	require.Len(t, rec.logs, 1)
}

func TestOnMessageCreate_ActionLog_NoDeleteNoTimeout(t *testing.T) {
	m, rec, images := newTestModule(t, enabledKV("log"))
	seedGradient(t, m)
	images["grad"] = encodePNG(t, makeGradient(256, 256))

	m.OnMessageCreate(nil, imageMessage("grad", "scam.png", "image/png", len(images["grad"])))

	require.Empty(t, rec.deleted)
	require.Empty(t, rec.timeouts)
	require.Len(t, rec.logs, 1)
}

func TestOnMessageCreate_NoMatch(t *testing.T) {
	m, rec, images := newTestModule(t, enabledKV("timeout"))
	seedGradient(t, m)
	images["check"] = encodePNG(t, makeChecker(256, 256, 8))

	m.OnMessageCreate(nil, imageMessage("check", "ok.png", "image/png", len(images["check"])))

	require.Empty(t, rec.deleted)
	require.Empty(t, rec.timeouts)
	require.Empty(t, rec.logs)
}

func TestOnMessageCreate_ChecksEveryImage(t *testing.T) {
	m, rec, images := newTestModule(t, enabledKV("timeout"))
	seedGradient(t, m)
	images["check"] = encodePNG(t, makeChecker(256, 256, 8)) // benign, first
	images["grad"] = encodePNG(t, makeGradient(256, 256))    // scam, second

	e := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "M1", ChannelID: "C1", GuildID: "G1",
		Author: &discordgo.User{ID: "U1"},
		Attachments: []*discordgo.MessageAttachment{
			{URL: "check", Filename: "a.png", ContentType: "image/png", Size: len(images["check"])},
			{URL: "grad", Filename: "b.png", ContentType: "image/png", Size: len(images["grad"])},
		},
	}}
	m.OnMessageCreate(nil, e)

	require.Equal(t, []string{"M1"}, rec.deleted)
	require.Len(t, rec.timeouts, 1)
}

func TestOnMessageCreate_Skips(t *testing.T) {
	grad := encodePNG(t, makeGradient(256, 256))

	t.Run("disabled", func(t *testing.T) {
		m, rec, images := newTestModule(t, map[string]interface{}{"scamguard_log_channel_id": enabledLog})
		seedGradient(t, m)
		images["grad"] = grad
		m.OnMessageCreate(nil, imageMessage("grad", "scam.png", "image/png", len(grad)))
		require.Empty(t, rec.deleted)
		require.Empty(t, rec.timeouts)
	})

	t.Run("bot author", func(t *testing.T) {
		m, rec, images := newTestModule(t, enabledKV("timeout"))
		seedGradient(t, m)
		images["grad"] = grad
		e := imageMessage("grad", "scam.png", "image/png", len(grad))
		e.Author.Bot = true
		m.OnMessageCreate(nil, e)
		require.Empty(t, rec.deleted)
	})

	t.Run("moderator author", func(t *testing.T) {
		m, rec, images := newTestModule(t, enabledKV("timeout"))
		seedGradient(t, m)
		images["grad"] = grad
		rec.isMod = true
		m.OnMessageCreate(nil, imageMessage("grad", "scam.png", "image/png", len(grad)))
		require.Empty(t, rec.deleted)
		require.Empty(t, rec.timeouts)
	})

	t.Run("no attachments", func(t *testing.T) {
		m, rec, _ := newTestModule(t, enabledKV("timeout"))
		seedGradient(t, m)
		e := &discordgo.MessageCreate{Message: &discordgo.Message{
			ID: "M1", ChannelID: "C1", GuildID: "G1",
			Author: &discordgo.User{ID: "U1"},
		}}
		m.OnMessageCreate(nil, e)
		require.Empty(t, rec.deleted)
	})

	t.Run("not in guild", func(t *testing.T) {
		m, rec, images := newTestModule(t, enabledKV("timeout"))
		seedGradient(t, m)
		images["grad"] = grad
		e := imageMessage("grad", "scam.png", "image/png", len(grad))
		e.GuildID = ""
		m.OnMessageCreate(nil, e)
		require.Empty(t, rec.deleted)
	})

	t.Run("oversize attachment skipped", func(t *testing.T) {
		m, rec, images := newTestModule(t, enabledKV("timeout"))
		seedGradient(t, m)
		images["grad"] = grad
		m.OnMessageCreate(nil, imageMessage("grad", "scam.png", "image/png", maxImageBytes+1))
		require.Empty(t, rec.deleted)
	})
}

func TestOnMessageCreate_NilSafe(t *testing.T) {
	m, _, _ := newTestModule(t, enabledKV("timeout"))
	require.NotPanics(t, func() {
		m.OnMessageCreate(nil, &discordgo.MessageCreate{Message: &discordgo.Message{}})
	})
}

// ---------------------------------------------------------------------------
// Mark as Scam Image
// ---------------------------------------------------------------------------

func TestMarkMessageScam(t *testing.T) {
	m, _, images := newTestModule(t, nil)
	images["grad"] = encodePNG(t, makeGradient(256, 256))

	msg := &discordgo.Message{
		ID: "T1", ChannelID: "C1",
		Attachments: []*discordgo.MessageAttachment{{URL: "grad", Filename: "scam.png", ContentType: "image/png", Size: len(images["grad"])}},
	}

	res := m.markMessageScam(msg, "mod1")
	require.Equal(t, 1, res.images)
	require.Equal(t, 1, res.added)
	require.Equal(t, 0, res.known)
	require.Equal(t, 0, res.failed)
	require.Equal(t, 1, m.hashCount())

	// Marking the same image again -> already known.
	res = m.markMessageScam(msg, "mod1")
	require.Equal(t, 1, res.images)
	require.Equal(t, 0, res.added)
	require.Equal(t, 1, res.known)
}

func TestMarkMessageScam_NonImage(t *testing.T) {
	m, _, _ := newTestModule(t, nil)
	msg := &discordgo.Message{
		ID: "T1", ChannelID: "C1",
		Attachments: []*discordgo.MessageAttachment{{URL: "x", Filename: "notes.txt", ContentType: "text/plain"}},
	}
	res := m.markMessageScam(msg, "mod1")
	require.Equal(t, 0, res.images)
	require.Equal(t, 0, res.added)
}

func TestMarkMessageScam_MultipleImages(t *testing.T) {
	m, _, images := newTestModule(t, nil)
	images["grad"] = encodePNG(t, makeGradient(256, 256))
	images["check"] = encodePNG(t, makeChecker(256, 256, 8))

	msg := &discordgo.Message{
		ID: "T1", ChannelID: "C1",
		Attachments: []*discordgo.MessageAttachment{
			{URL: "grad", Filename: "a.png", ContentType: "image/png", Size: len(images["grad"])},
			{URL: "check", Filename: "b.png", ContentType: "image/png", Size: len(images["check"])},
		},
	}

	res := m.markMessageScam(msg, "mod1")
	require.Equal(t, 2, res.images)
	require.Equal(t, 2, res.added)
	require.Equal(t, 0, res.known)
	require.Equal(t, 2, m.hashCount())

	// Both images are now independently detectable.
	gradH, err := computeHash(images["grad"])
	require.NoError(t, err)
	_, ok := m.matchHash(gradH, 8)
	require.True(t, ok)
	checkH, err := computeHash(images["check"])
	require.NoError(t, err)
	_, ok = m.matchHash(checkH, 8)
	require.True(t, ok)
}

// ---------------------------------------------------------------------------
// Unmark Scam Image
// ---------------------------------------------------------------------------

func TestUnmarkMessageScam(t *testing.T) {
	m, _, images := newTestModule(t, nil)
	images["grad"] = encodePNG(t, makeGradient(256, 256))

	mark := &discordgo.Message{ID: "T1", ChannelID: "C1", Attachments: []*discordgo.MessageAttachment{
		{URL: "grad", Filename: "scam.png", ContentType: "image/png", Size: len(images["grad"])},
	}}
	require.Equal(t, 1, m.markMessageScam(mark, "mod1").added)
	require.Equal(t, 1, m.hashCount())

	res := m.unmarkMessageScam(mark)
	require.Equal(t, 1, res.images)
	require.Equal(t, 1, res.removed)
	require.Equal(t, 0, res.missed)
	require.Equal(t, 0, res.failed)
	require.Len(t, res.hashes, 1)
	require.Equal(t, 0, m.hashCount())

	// Unmarking again finds nothing to remove.
	res = m.unmarkMessageScam(mark)
	require.Equal(t, 0, res.removed)
	require.Equal(t, 1, res.missed)
}

func TestUnmarkMessageScam_ReencodedStillRemoves(t *testing.T) {
	m, _, images := newTestModule(t, nil)
	seedGradient(t, m) // blocklist holds a PNG-derived gradient hash
	require.Equal(t, 1, m.hashCount())

	// A re-encoded (JPEG) copy of the same image still clears the entry.
	images["grad"] = encodeJPEG(t, makeGradient(256, 256), 80)
	msg := &discordgo.Message{ID: "T1", ChannelID: "C1", Attachments: []*discordgo.MessageAttachment{
		{URL: "grad", Filename: "same.jpg", ContentType: "image/jpeg", Size: len(images["grad"])},
	}}
	res := m.unmarkMessageScam(msg)
	require.Equal(t, 1, res.removed)
	require.Equal(t, 0, m.hashCount())
}

func TestUnmarkMessageScam_NoMatch(t *testing.T) {
	m, _, images := newTestModule(t, nil)
	seedGradient(t, m)
	images["check"] = encodePNG(t, makeChecker(256, 256, 8))

	msg := &discordgo.Message{ID: "T1", ChannelID: "C1", Attachments: []*discordgo.MessageAttachment{
		{URL: "check", Filename: "ok.png", ContentType: "image/png", Size: len(images["check"])},
	}}
	res := m.unmarkMessageScam(msg)
	require.Equal(t, 1, res.images)
	require.Equal(t, 0, res.removed)
	require.Equal(t, 1, res.missed)
	require.Equal(t, 1, m.hashCount()) // gradient entry untouched
}

func TestUnmarkMessageScam_MultipleImages(t *testing.T) {
	m, _, images := newTestModule(t, nil)
	images["grad"] = encodePNG(t, makeGradient(256, 256))
	images["check"] = encodePNG(t, makeChecker(256, 256, 8))

	msg := &discordgo.Message{ID: "T1", ChannelID: "C1", Attachments: []*discordgo.MessageAttachment{
		{URL: "grad", Filename: "a.png", ContentType: "image/png", Size: len(images["grad"])},
		{URL: "check", Filename: "b.png", ContentType: "image/png", Size: len(images["check"])},
	}}
	require.Equal(t, 2, m.markMessageScam(msg, "mod1").added)
	require.Equal(t, 2, m.hashCount())

	res := m.unmarkMessageScam(msg)
	require.Equal(t, 2, res.images)
	require.Equal(t, 2, res.removed)
	require.Equal(t, 0, m.hashCount())
}

func TestLogUnmark_PostsAudit(t *testing.T) {
	m, rec, _ := newTestModule(t, enabledKV("timeout"))
	m.logUnmark(nil, "mod1", unmarkResult{removed: 2, hashes: []string{"p:aa", "p:bb"}})
	require.Len(t, rec.logs, 1)
	require.Equal(t, "🧹 Scam Image Unmarked", rec.logs[0].Title)
}

// ---------------------------------------------------------------------------
// Module plumbing
// ---------------------------------------------------------------------------

func TestRegister_RegistersMarkCommand(t *testing.T) {
	cfg := config.NewMockConfig(nil)
	m := New(&types.Dependencies{Config: cfg})
	cmds := map[string]*types.Command{}
	m.Register(cmds, &types.Dependencies{Config: cfg})

	for _, name := range []string{markScamCommandName, unmarkScamCommandName} {
		cmd, ok := cmds[name]
		require.True(t, ok, "expected %q to be registered", name)
		require.Equal(t, discordgo.MessageApplicationCommand, cmd.ApplicationCommand.Type)
		require.NotNil(t, cmd.ApplicationCommand.DefaultMemberPermissions)
		require.Equal(t, int64(discordgo.PermissionBanMembers), *cmd.ApplicationCommand.DefaultMemberPermissions)
		require.NotNil(t, cmd.HandlerFunc)
	}
}

func TestService_IsNil(t *testing.T) {
	m := New(&types.Dependencies{Config: config.NewMockConfig(nil)})
	require.Nil(t, m.Service())
}

func TestRolesGrantModerator(t *testing.T) {
	const modBits = discordgo.PermissionBanMembers | discordgo.PermissionAdministrator
	guild := &discordgo.Guild{
		ID: "G",
		Roles: []*discordgo.Role{
			{ID: "G", Permissions: discordgo.PermissionViewChannel}, // @everyone
			{ID: "mod", Permissions: discordgo.PermissionBanMembers},
			{ID: "admin", Permissions: discordgo.PermissionAdministrator},
			{ID: "plain", Permissions: discordgo.PermissionSendMessages},
		},
	}

	require.False(t, rolesGrantModerator(guild, []string{"plain"}, modBits))
	require.False(t, rolesGrantModerator(guild, nil, modBits))
	require.True(t, rolesGrantModerator(guild, []string{"mod"}, modBits))
	require.True(t, rolesGrantModerator(guild, []string{"admin"}, modBits))
	require.True(t, rolesGrantModerator(guild, []string{"plain", "mod"}, modBits))

	everyoneBan := &discordgo.Guild{
		ID:    "G",
		Roles: []*discordgo.Role{{ID: "G", Permissions: discordgo.PermissionBanMembers}},
	}
	require.True(t, rolesGrantModerator(everyoneBan, nil, modBits))
}

func TestModule_DBPersistsAndReloads(t *testing.T) {
	db, err := database.NewDB(filepath.Join(t.TempDir(), "scam.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := config.NewMockConfig(nil)
	m := New(&types.Dependencies{Config: cfg, DB: db})
	require.Equal(t, 0, m.hashCount())

	h, err := computeHash(encodePNG(t, makeGradient(128, 128)))
	require.NoError(t, err)
	added, err := m.addKnownHash(hashString(h), "mod1", "command")
	require.NoError(t, err)
	require.True(t, added)
	require.Equal(t, 1, m.hashCount())

	// Adding the same hash again is a no-op (DB UNIQUE + dedupe).
	added, err = m.addKnownHash(hashString(h), "mod1", "command")
	require.NoError(t, err)
	require.False(t, added)
	require.Equal(t, 1, m.hashCount())

	// A fresh module backed by the same DB reloads the persisted hash.
	m2 := New(&types.Dependencies{Config: cfg, DB: db})
	require.Equal(t, 1, m2.hashCount())
	_, ok := m2.matchHash(h, 0)
	require.True(t, ok)

	// Removal persists too: a fresh module sees an empty blocklist.
	removed, err := m2.removeKnownHash(hashString(h))
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, 0, m2.hashCount())

	m3 := New(&types.Dependencies{Config: cfg, DB: db})
	require.Equal(t, 0, m3.hashCount())
}
