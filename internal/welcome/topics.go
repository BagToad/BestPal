package welcome

import (
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// TopicService manages rotating channel topics
type TopicService struct {
	session     *discordgo.Session
	channelID   string
	topics      []string
	currentIdx  int
	lastRotated time.Time
	mutex       sync.RWMutex
}

// NewTopicService creates a new TopicService instance
func NewTopicService(session *discordgo.Session, channelID string) *TopicService {
	topics := []string{
		"Games you have but don't have anyone to play with",
		"Games you want to try but can't justify buying",
		"Favorite single player game",
		"Favorite co-op games",
		"Games you've been enjoying lately",
		"Games that help you unwind/chill games",
	}

	return &TopicService{
		session:     session,
		channelID:   channelID,
		topics:      topics,
		currentIdx:  0,
		lastRotated: time.Now(),
	}
}

// GetCurrentTopic returns the current topic in a thread-safe manner
func (ts *TopicService) GetCurrentTopic() string {
	ts.mutex.RLock()
	defer ts.mutex.RUnlock()
	
	if len(ts.topics) == 0 {
		return ""
	}
	return ts.topics[ts.currentIdx]
}

// RotateTopic rotates to the next topic and updates the Discord channel topic
func (ts *TopicService) RotateTopic() error {
	ts.mutex.Lock()
	defer ts.mutex.Unlock()

	if len(ts.topics) == 0 {
		return nil
	}

	// Move to next topic
	ts.currentIdx = (ts.currentIdx + 1) % len(ts.topics)
	ts.lastRotated = time.Now()

	// Update Discord channel topic if we have a valid channel ID and session
	if ts.channelID != "" && ts.session != nil && ts.session.State != nil {
		currentTopic := ts.topics[ts.currentIdx]
		_, err := ts.session.ChannelEdit(ts.channelID, &discordgo.ChannelEdit{
			Topic: currentTopic,
		})
		return err
	}

	return nil
}

// ShouldRotate checks if enough time has passed to rotate (1 hour)
func (ts *TopicService) ShouldRotate() bool {
	ts.mutex.RLock()
	defer ts.mutex.RUnlock()
	
	return time.Since(ts.lastRotated) >= time.Hour
}

// GetLastRotated returns when the topic was last rotated
func (ts *TopicService) GetLastRotated() time.Time {
	ts.mutex.RLock()
	defer ts.mutex.RUnlock()
	
	return ts.lastRotated
}