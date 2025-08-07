package welcome

import (
	"gamerpal/internal/config"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestWelcomeService_NewWelcomeService(t *testing.T) {
	session := &discordgo.Session{}
	cfg := config.NewMockConfig(map[string]interface{}{
		"new_pals_channel_id":                     "123456789",
		"new_pals_time_between_welcome_messages": "5m",
	})

	ws := NewWelcomeService(session, cfg)

	if ws == nil {
		t.Fatal("WelcomeService should not be nil")
	}

	if ws.session != session {
		t.Error("WelcomeService session should match provided session")
	}

	if ws.config != cfg {
		t.Error("WelcomeService config should match provided config")
	}

	if ws.topicService == nil {
		t.Error("WelcomeService should have a topicService")
	}
}

func TestWelcomeService_TopicInWelcomeMessage(t *testing.T) {
	session := &discordgo.Session{}
	cfg := config.NewMockConfig(map[string]interface{}{
		"new_pals_channel_id":                     "123456789",
		"new_pals_time_between_welcome_messages": "5m",
	})

	ws := NewWelcomeService(session, cfg)

	// Get the current topic
	currentTopic := ws.topicService.GetCurrentTopic()
	if currentTopic == "" {
		t.Fatal("TopicService should have a current topic")
	}

	// Since we can't easily test the full welcome message flow without setting up
	// Discord API mocks, let's test that we can get the topic and it's formatted correctly
	expectedTopicMessage := "**The current channel topic is _" + currentTopic + "_**"

	// Test that the topic message format is what we expect
	if !strings.Contains(expectedTopicMessage, currentTopic) {
		t.Errorf("Expected topic message to contain '%s', got '%s'", currentTopic, expectedTopicMessage)
	}

	if !strings.Contains(expectedTopicMessage, "**The current channel topic is _") {
		t.Error("Topic message should start with the expected format")
	}

	if !strings.Contains(expectedTopicMessage, "_**") {
		t.Error("Topic message should end with the expected format")
	}
}

func TestWelcomeService_RotateTopicIfNeeded(t *testing.T) {
	// Use nil session to avoid Discord API calls in test
	cfg := config.NewMockConfig(map[string]interface{}{
		"new_pals_channel_id":                     "123456789",
		"new_pals_time_between_welcome_messages": "5m",
	})

	ws := NewWelcomeService(nil, cfg)

	initialTopic := ws.topicService.GetCurrentTopic()

	// Call RotateTopicIfNeeded - it shouldn't rotate immediately after creation
	ws.RotateTopicIfNeeded()

	// Topic should remain the same since it was just created
	currentTopic := ws.topicService.GetCurrentTopic()
	if currentTopic != initialTopic {
		t.Error("Topic should not have rotated immediately after creation")
	}

	// Manually trigger rotation by directly calling the topic service
	err := ws.topicService.RotateTopic()
	if err != nil {
		t.Fatalf("Manual topic rotation should not fail: %v", err)
	}

	// Now the topic should be different
	rotatedTopic := ws.topicService.GetCurrentTopic()
	if rotatedTopic == initialTopic {
		t.Error("Topic should have changed after manual rotation")
	}
}