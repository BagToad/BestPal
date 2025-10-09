package log

import (
	"archive/zip"
	"bufio"
	"fmt"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface for the log command
type Module struct {
	config *config.Config
}

// Register adds the log command to the command map
func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	m.config = deps.Config

	var adminPerms int64 = discordgo.PermissionAdministrator

	cmds["log"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "log",
			Description:              "Download bot logs (SuperAdmin only)",
			DefaultMemberPermissions: &adminPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextBotDM, discordgo.InteractionContextPrivateChannel},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "download",
					Description: "Download all logs",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "latest",
					Description: "Show the latest log entries",
				},
			},
		},
		HandlerFunc: m.handleLog,
	}
}

// handleLog processes the /log command with subcommands for downloading logs
// Only accessible to super admins in DM context
func (m *Module) handleLog(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !utils.IsSuperAdmin(i.User.ID, m.config) {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ You do not have permission to use this command.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Defer to show thinking
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		m.sendErrorFollowup(s, i, "❌ No subcommand provided.")
		return
	}

	switch options[0].Name {
	case "download":
		m.handleLogDownload(s, i)
	case "latest":
		m.handleLogLatest(s, i)
	default:
		m.sendErrorFollowup(s, i, "❌ Unknown subcommand.")
	}
}

func (m *Module) handleLogDownload(s *discordgo.Session, i *discordgo.InteractionCreate) {
	logDir := m.config.GetLogDir()
	if logDir == "" {
		m.sendErrorFollowup(s, i, "❌ Log directory is not configured.")
		return
	}

	// Check if log directory exists
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		m.sendErrorFollowup(s, i, "❌ Log directory does not exist.")
		return
	}

	// Get all log files
	logFiles, err := m.getLogFiles(logDir)
	if err != nil {
		m.config.Logger.Errorf("Error getting log files: %v", err)
		m.sendErrorFollowup(s, i, "❌ Error reading log files.")
		return
	}

	if len(logFiles) == 0 {
		m.sendErrorFollowup(s, i, "❌ No log files found.")
		return
	}

	// Create a temporary zip file
	zipPath := filepath.Join(os.TempDir(), fmt.Sprintf("gamerpal_logs_%s.zip", time.Now().Format("2006-01-02_15-04-05")))
	defer func() {
		if err := os.Remove(zipPath); err != nil {
			m.config.Logger.Warnf("could not remove temp zip %s: %v", zipPath, err)
		}
	}() // Clean up after sending

	err = m.createLogZip(logFiles, zipPath)
	if err != nil {
		m.config.Logger.Errorf("Error creating zip file: %v", err)
		m.sendErrorFollowup(s, i, "❌ Error creating log archive.")
		return
	}

	// Send the zip file
	file, err := os.Open(zipPath)
	if err != nil {
		m.config.Logger.Errorf("Error opening zip file: %v", err)
		m.sendErrorFollowup(s, i, "❌ Error opening log archive.")
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			m.config.Logger.Warnf("error closing zip file: %v", err)
		}
	}()

	_, err = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr(fmt.Sprintf("📁 Log files archive containing %d files:", len(logFiles))),
		Files: []*discordgo.File{
			{
				Name:   filepath.Base(zipPath),
				Reader: file,
			},
		},
	})

	if err != nil {
		m.config.Logger.Errorf("Error sending log archive: %v", err)
		m.sendErrorFollowup(s, i, "❌ Error sending log archive.")
	}
}

func (m *Module) handleLogLatest(s *discordgo.Session, i *discordgo.InteractionCreate) {
	logDir := m.config.GetLogDir()
	if logDir == "" {
		m.sendErrorFollowup(s, i, "❌ Log directory is not configured.")
		return
	}

	// Get the latest log file
	latestLogFile, err := m.getLatestLogFile(logDir)
	if err != nil {
		m.config.Logger.Errorf("Error getting latest log file: %v", err)
		m.sendErrorFollowup(s, i, "❌ Error finding latest log file.")
		return
	}

	if latestLogFile == "" {
		m.sendErrorFollowup(s, i, "❌ No log files found.")
		return
	}

	// Read the last 500 lines
	lines, err := m.getLastNLines(latestLogFile, 500)
	if err != nil {
		m.config.Logger.Errorf("Error reading log file: %v", err)
		m.sendErrorFollowup(s, i, "❌ Error reading log file.")
		return
	}

	// Create a temporary file with the content
	tempPath := filepath.Join(os.TempDir(), fmt.Sprintf("gamerpal_latest_%s.txt", time.Now().Format("2006-01-02_15-04-05")))
	defer func() {
		if err := os.Remove(tempPath); err != nil {
			m.config.Logger.Warnf("could not remove temp log file %s: %v", tempPath, err)
		}
	}() // Clean up after sending

	err = os.WriteFile(tempPath, []byte(strings.Join(lines, "\n")), 0644)
	if err != nil {
		m.config.Logger.Errorf("Error creating temp file: %v", err)
		m.sendErrorFollowup(s, i, "❌ Error creating log file.")
		return
	}

	// Send the text file
	file, err := os.Open(tempPath)
	if err != nil {
		m.config.Logger.Errorf("Error opening temp file: %v", err)
		m.sendErrorFollowup(s, i, "❌ Error opening log file.")
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			m.config.Logger.Warnf("error closing temp log file: %v", err)
		}
	}()

	_, err = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr(fmt.Sprintf("📄 Latest %d lines from %s:", len(lines), filepath.Base(latestLogFile))),
		Files: []*discordgo.File{
			{
				Name:   filepath.Base(tempPath),
				Reader: file,
			},
		},
	})

	if err != nil {
		m.config.Logger.Errorf("Error sending latest log: %v", err)
		m.sendErrorFollowup(s, i, "❌ Error sending log file.")
	}
}

// getLogFiles returns a sorted list of log files in the directory
func (m *Module) getLogFiles(logDir string) ([]string, error) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return nil, err
	}

	var logFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		// Check if it's a log file (ends with .log)
		if strings.HasSuffix(name, ".log") {
			logFiles = append(logFiles, filepath.Join(logDir, name))
		}
	}

	// Sort files by name (which should be chronological due to date format)
	sort.Strings(logFiles)
	return logFiles, nil
}

// getLatestLogFile returns the path to the most recent log file
func (m *Module) getLatestLogFile(logDir string) (string, error) {
	logFiles, err := m.getLogFiles(logDir)
	if err != nil {
		return "", err
	}

	if len(logFiles) == 0 {
		return "", fmt.Errorf("no log files found")
	}

	// Return the last file in the sorted list (most recent)
	return logFiles[len(logFiles)-1], nil
}

// createLogZip creates a zip archive containing all the log files
func (m *Module) createLogZip(logFiles []string, zipPath string) error {
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := zipFile.Close(); err != nil {
			m.config.Logger.Warnf("error closing zip writer file: %v", err)
		}
	}()

	zipWriter := zip.NewWriter(zipFile)
	defer func() {
		if err := zipWriter.Close(); err != nil {
			m.config.Logger.Warnf("error closing zip writer: %v", err)
		}
	}()

	for _, logFile := range logFiles {
		err := m.addFileToZip(zipWriter, logFile)
		if err != nil {
			return fmt.Errorf("error adding %s to zip: %w", logFile, err)
		}
	}

	return nil
}

// addFileToZip adds a single file to the zip archive
func (m *Module) addFileToZip(zipWriter *zip.Writer, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			m.config.Logger.Warnf("error closing log file during zip: %v", err)
		}
	}()

	// Get file info for the header
	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}

	// Create a file header
	header, err := zip.FileInfoHeader(fileInfo)
	if err != nil {
		return err
	}

	// Use only the filename in the zip, not the full path
	header.Name = filepath.Base(filePath)
	header.Method = zip.Deflate

	// Create the file in the zip
	writer, err := zipWriter.CreateHeader(header)
	if err != nil {
		return err
	}

	// Copy the file content
	_, err = io.Copy(writer, file)
	return err
}

// getLastNLines reads the last N lines from a file
func (m *Module) getLastNLines(filePath string, n int) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			m.config.Logger.Warnf("error closing file while tailing: %v", err)
		}
	}()

	var lines []string
	scanner := bufio.NewScanner(file)

	// Read all lines into memory (for small-medium files this is fine)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Return the last N lines
	if len(lines) <= n {
		return lines, nil
	}

	return lines[len(lines)-n:], nil
}

// sendErrorFollowup sends an error message as a followup
func (m *Module) sendErrorFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr(message),
	})
}
