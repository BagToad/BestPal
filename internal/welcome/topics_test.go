package welcome

import (
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestTopicService_NewTopicService(t *testing.T) {
	session := &discordgo.Session{}
	channelID := "123456789"
	
	ts := NewTopicService(session, channelID)
	
	if ts == nil {
		t.Fatal("TopicService should not be nil")
	}
	
	if ts.session != session {
		t.Error("TopicService session should match provided session")
	}
	
	if ts.channelID != channelID {
		t.Error("TopicService channelID should match provided channelID")
	}
	
	if len(ts.topics) != 6 {
		t.Errorf("Expected 6 topics, got %d", len(ts.topics))
	}
	
	if ts.currentIdx != 0 {
		t.Errorf("Expected initial currentIdx to be 0, got %d", ts.currentIdx)
	}
}

func TestTopicService_GetCurrentTopic(t *testing.T) {
	ts := NewTopicService(nil, "")
	
	expectedTopic := "Games you have but don't have anyone to play with"
	currentTopic := ts.GetCurrentTopic()
	
	if currentTopic != expectedTopic {
		t.Errorf("Expected current topic to be '%s', got '%s'", expectedTopic, currentTopic)
	}
}

func TestTopicService_RotateTopic(t *testing.T) {
	ts := NewTopicService(nil, "") // No session/channel for this test
	
	initialTopic := ts.GetCurrentTopic()
	
	err := ts.RotateTopic()
	if err != nil {
		t.Fatalf("RotateTopic should not return error: %v", err)
	}
	
	newTopic := ts.GetCurrentTopic()
	if newTopic == initialTopic {
		t.Error("Topic should have changed after rotation")
	}
	
	expectedSecondTopic := "Games you want to try but can't justify buying"
	if newTopic != expectedSecondTopic {
		t.Errorf("Expected second topic to be '%s', got '%s'", expectedSecondTopic, newTopic)
	}
}

func TestTopicService_RotateAllTopics(t *testing.T) {
	ts := NewTopicService(nil, "")
	
	expectedTopics := []string{
		"Games you have but don't have anyone to play with",
		"Games you want to try but can't justify buying",
		"Favorite single player game",
		"Favorite co-op games",
		"Games you've been enjoying lately",
		"Games that help you unwind/chill games",
	}
	
	// Test that we can rotate through all topics and wrap around
	for i := 0; i < len(expectedTopics)*2; i++ {
		expectedIdx := i % len(expectedTopics)
		currentTopic := ts.GetCurrentTopic()
		
		if currentTopic != expectedTopics[expectedIdx] {
			t.Errorf("At iteration %d, expected topic '%s', got '%s'", 
				i, expectedTopics[expectedIdx], currentTopic)
		}
		
		err := ts.RotateTopic()
		if err != nil {
			t.Fatalf("RotateTopic should not return error at iteration %d: %v", i, err)
		}
	}
}

func TestTopicService_ShouldRotate(t *testing.T) {
	ts := NewTopicService(nil, "")
	
	// Initially should not need to rotate (just created)
	if ts.ShouldRotate() {
		t.Error("Should not need to rotate immediately after creation")
	}
	
	// Manually set lastRotated to more than an hour ago
	ts.mutex.Lock()
	ts.lastRotated = time.Now().Add(-2 * time.Hour)
	ts.mutex.Unlock()
	
	if !ts.ShouldRotate() {
		t.Error("Should need to rotate after more than an hour")
	}
}

func TestTopicService_GetLastRotated(t *testing.T) {
	ts := NewTopicService(nil, "")
	
	beforeRotate := time.Now()
	
	err := ts.RotateTopic()
	if err != nil {
		t.Fatalf("RotateTopic should not return error: %v", err)
	}
	
	afterRotate := time.Now()
	lastRotated := ts.GetLastRotated()
	
	if lastRotated.Before(beforeRotate) || lastRotated.After(afterRotate) {
		t.Error("GetLastRotated should return a time between before and after rotation")
	}
}

func TestTopicService_ConcurrencySafety(t *testing.T) {
	ts := NewTopicService(nil, "")
	
	// Test concurrent access
	done := make(chan bool, 100)
	
	// Start multiple goroutines that access the topic service
	for i := 0; i < 50; i++ {
		go func() {
			defer func() { done <- true }()
			_ = ts.GetCurrentTopic()
			_ = ts.ShouldRotate()
			_ = ts.GetLastRotated()
		}()
		
		go func() {
			defer func() { done <- true }()
			_ = ts.RotateTopic()
		}()
	}
	
	// Wait for all goroutines to complete
	for i := 0; i < 100; i++ {
		<-done
	}
	
	// If we get here without deadlock or panic, concurrency safety is working
}