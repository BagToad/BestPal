package fetchintros

import (
	"encoding/json"
	"fmt"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/database"
	"gamerpal/internal/utils"
	"os"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface
type Module struct {
	deps *types.Dependencies
}

// New creates a new fetch-intros module
func New(deps *types.Dependencies) *Module {
	return &Module{
		deps: deps,
	}
}

// Register adds the fetch-intros command
func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	var adminPerms int64 = discordgo.PermissionAdministrator

	cmds["fetch-intros"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "fetch-intros",
			Description:              "Fetch all introduction posts from the forum and store in database (Admin only)",
			DefaultMemberPermissions: &adminPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		},
		HandlerFunc: m.handleFetchIntros,
	}
}

// Service returns nil (no background service needed)
func (m *Module) Service() types.ModuleService {
	return nil
}

// handleFetchIntros handles the /fetch-intros command
func (m *Module) handleFetchIntros(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !utils.IsSuperAdmin(i.User.ID, m.deps.Config) {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ You do not have permission to use this command.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Defer response (operation will take time)
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		m.deps.Config.Logger.Errorf("Error deferring response: %v", err)
		return
	}

	// Check if database is available
	if m.deps.DB == nil {
		m.editResponse(s, i, "❌ Error: Database is not available")
		return
	}

	// Get forum channel ID from config
	forumID := m.deps.Config.GetGamerPalsIntroductionsForumChannelID()
	if forumID == "" {
		m.editResponse(s, i, "❌ Error: Forum channel ID not configured")
		return
	}

	// Get guild ID from interaction
	guildID := i.GuildID
	if guildID == "" {
		m.editResponse(s, i, "❌ Error: This command can only be used in a guild")
		return
	}

	// Fetch and store threads
	summary, err := m.fetchAndStoreThreads(s, guildID, forumID)
	if err != nil {
		m.editResponse(s, i, fmt.Sprintf("❌ Error fetching threads: %v", err))
		return
	}

	// Attach the database file
	dbPath := m.deps.Config.GetDatabasePath()
	dbFile, err := os.Open(dbPath)
	if err != nil {
		m.deps.Config.Logger.Errorf("Failed to open database file: %v", err)
		m.editResponse(s, i, summary+"\n\n⚠️ Note: Could not attach database file")
		return
	}
	defer dbFile.Close()

	files := []*discordgo.File{
		{
			Name:        "bestpal.db",
			ContentType: "application/x-sqlite3",
			Reader:      dbFile,
		},
	}

	summary += "\n\n📎 Database file attached (bestpal.db)"
	m.editResponseWithFiles(s, i, summary, files)
}

// fetchAndStoreThreads fetches threads from forum and stores in database
func (m *Module) fetchAndStoreThreads(s *discordgo.Session, guildID, forumID string) (string, error) {
	// Use ForumCache to get all threads
	threads, ok := m.deps.ForumCache.ListThreads(forumID)
	if !ok || len(threads) == 0 {
		// Cache miss or empty cache - refresh forum to ensure we see all threads
		err := m.deps.ForumCache.RefreshForum(guildID, forumID)
		if err != nil {
			return "", fmt.Errorf("failed to refresh forum cache: %w", err)
		}
		threads, ok = m.deps.ForumCache.ListThreads(forumID)
		if !ok || len(threads) == 0 {
			// After a refresh, treat an empty result as "no threads found"
			return "⚠️ No threads found in forum", nil
		}
	}

	// Fetch full content for each thread
	successCount := 0
	errorCount := 0

	for _, meta := range threads {
		// Rate limit at the start of each iteration
		time.Sleep(100 * time.Millisecond)

		// Fetch the original message that created the thread
		// In Discord forum threads, the thread ID is the same as the first message ID
		firstMessage, err := s.ChannelMessage(meta.ID, meta.ID)
		if err != nil {
			m.deps.Config.Logger.Errorf("Failed to fetch message for thread %s: %v", meta.ID, err)
			errorCount++
			continue
		}

		// Get username
		username := "Unknown"
		if firstMessage.Author != nil {
			username = firstMessage.Author.Username
		}

		// Fetch thread to get applied tags
		appliedTagsJSON := "[]"
		threadChannel, err := s.Channel(meta.ID)
		if err != nil {
			m.deps.Config.Logger.Errorf("Failed to fetch thread channel %s for tags: %v", meta.ID, err)
			// Continue without tags - not a fatal error
		} else if threadChannel != nil && len(threadChannel.AppliedTags) > 0 {
			tagsBytes, _ := json.Marshal(threadChannel.AppliedTags)
			appliedTagsJSON = string(tagsBytes)
		}

		// Create thread record
		thread := &database.IntroductionThread{
			ThreadID:            meta.ID,
			UserID:              meta.OwnerID,
			Username:            username,
			ThreadTitle:         meta.Name,
			FirstMessageContent: firstMessage.Content,
			AppliedTags:         appliedTagsJSON,
			CreatedAt:           meta.CreatedAt,
		}

		// Save to database
		err = m.deps.DB.SaveIntroductionThread(thread)
		if err != nil {
			m.deps.Config.Logger.Errorf("Failed to save thread %s: %v", meta.ID, err)
			errorCount++
			continue
		}

		successCount++
	}

	// Build summary message
	summary := fmt.Sprintf(
		"✅ **Fetch Complete**\n\n"+
			"📊 **Summary:**\n"+
			"- Total threads found: **%d**\n"+
			"- Successfully stored: **%d**\n"+
			"- Errors: **%d**",
		len(threads), successCount, errorCount,
	)

	return summary, nil
}

// editResponse helper to update deferred response
func (m *Module) editResponse(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &content,
	})
	if err != nil {
		m.deps.Config.Logger.Errorf("Error editing response: %v", err)
	}
}

// editResponseWithFiles helper to update deferred response with file attachments
func (m *Module) editResponseWithFiles(s *discordgo.Session, i *discordgo.InteractionCreate, content string, files []*discordgo.File) {
	_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &content,
		Files:   files,
	})
	if err != nil {
		m.deps.Config.Logger.Errorf("Error editing response with files: %v", err)
	}
}
