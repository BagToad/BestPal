package lfgpanel

import (
	"testing"
	"time"

	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
)

type fakeSession struct {
	channels map[string]*discordgo.Channel
	edits    []string
	sends    []string
	deletes  []string
	messages map[string]*discordgo.Message
}

func newFakeSession() *fakeSession {
	return &fakeSession{channels: map[string]*discordgo.Channel{}, messages: map[string]*discordgo.Message{}}
}

func (f *fakeSession) Channel(id string, _ ...discordgo.RequestOption) (*discordgo.Channel, error) {
	return f.channels[id], nil
}
func (f *fakeSession) ChannelMessageDelete(chID, msgID string, _ ...discordgo.RequestOption) error {
	f.deletes = append(f.deletes, msgID)
	delete(f.messages, msgID)
	return nil
}
func (f *fakeSession) ChannelMessageEditComplex(m *discordgo.MessageEdit, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.edits = append(f.edits, m.ID)
	return &discordgo.Message{ID: m.ID, ChannelID: m.Channel}, nil
}
func (f *fakeSession) ChannelMessageSendEmbeds(chID string, embeds []*discordgo.MessageEmbed, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	id := time.Now().Format("150405.000000") + chID
	msg := &discordgo.Message{ID: id, ChannelID: chID, Embeds: embeds}
	f.messages[id] = msg
	f.sends = append(f.sends, id)
	return msg, nil
}

// Basic compile-time interface satisfaction
var _ Service = (*InMemoryService)(nil)

func TestUpsertAndRefreshLifecycle(t *testing.T) {
	cfg := config.NewMockConfig(map[string]interface{}{})
	svc := NewLFGPanelService(cfg)
	svc.SetupPanel("panel-chan")
	sess := newFakeSession()
	// create thread channels
	sess.channels["thread1"] = &discordgo.Channel{ID: "thread1", Name: "Game A"}
	sess.channels["thread2"] = &discordgo.Channel{ID: "thread2", Name: "Game B"}

	if err := svc.Upsert("thread1", "user1", "na", "msg1", 3); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := svc.Upsert("thread1", "user2", "na", "msg2", 2); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := svc.Upsert("thread2", "user3", "eu", "msg3", 4); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := svc.RefreshPanel(sess, time.Hour); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(sess.sends) == 0 {
		t.Fatalf("expected at least one message send")
	}

	firstSendCount := len(sess.sends)
	// Second refresh without change should edit, not send new if embed count stable
	if err := svc.RefreshPanel(sess, time.Hour); err != nil {
		t.Fatalf("refresh2: %v", err)
	}
	if len(sess.sends) != firstSendCount {
		t.Fatalf("expected no new sends, got %d vs %d", len(sess.sends), firstSendCount)
	}
	if len(sess.edits) == 0 {
		t.Fatalf("expected edits on stable refresh")
	}
}

func TestPruneExpired(t *testing.T) {
	cfg := config.NewMockConfig(map[string]interface{}{})
	svc := NewLFGPanelService(cfg)
	svc.SetupPanel("panel")
	sess := newFakeSession()
	sess.channels["thread1"] = &discordgo.Channel{ID: "thread1", Name: "Game"}

	if err := svc.Upsert("thread1", "user1", "na", "msg", 3); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Force entry old by manipulating internal map
	svc.mu.Lock()
	for _, m := range svc.entries {
		for _, e := range m {
			e.UpdatedAt = time.Now().Add(-2 * time.Hour)
		}
	}
	svc.mu.Unlock()
	if err := svc.RefreshPanel(sess, time.Hour); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// With new empty-state behavior we expect exactly 1 panel message containing the placeholder embed.
	if len(sess.messages) != 1 {
			t.Fatalf("expected 1 empty-state panel message after prune, got %d", len(sess.messages))
	}
	for _, m := range sess.messages {
		if len(m.Embeds) != 1 {
			t.Fatalf("expected exactly 1 embed in empty-state message, got %d", len(m.Embeds))
		}
		e := m.Embeds[0]
		if e.Title != "Looking NOW" {
			t.Fatalf("unexpected empty-state title: %s", e.Title)
		}
		wantDesc := "Nobody is on right now :zzz:"
		if e.Description != wantDesc {
			t.Fatalf("unexpected empty-state description: %q", e.Description)
		}
	}
}
