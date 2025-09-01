package commands

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// handleLog processes the /log command with subcommands for downloading logs
// Only accessible to super admins in DM context
func (h *SlashHandler) handleLog(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !utils.IsSuperAdmin(i.User.ID, h.config) {
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
		h.sendErrorFollowup(s, i, "❌ No subcommand provided.")
		return
	}

	switch options[0].Name {
	case "download":
		h.handleLogDownload(s, i)
	case "latest":
		h.handleLogLatest(s, i)
	default:
		h.sendErrorFollowup(s, i, "❌ Unknown subcommand.")
	}
}

func (h *SlashHandler) handleLogDownload(s *discordgo.Session, i *discordgo.InteractionCreate) {
	logDir := h.config.GetLogDir()
	if logDir == "" {
		h.sendErrorFollowup(s, i, "❌ Log directory is not configured.")
		return
	}

	// Check if log directory exists
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		h.sendErrorFollowup(s, i, "❌ Log directory does not exist.")
		return
	}

	// Get all log files
	logFiles, err := h.getLogFiles(logDir)
	if err != nil {
		h.config.Logger.Errorf("Error getting log files: %v", err)
		h.sendErrorFollowup(s, i, "❌ Error reading log files.")
		return
	}

	if len(logFiles) == 0 {
		h.sendErrorFollowup(s, i, "❌ No log files found.")
		return
	}

	// Create a temporary zip file
	zipPath := filepath.Join(os.TempDir(), fmt.Sprintf("gamerpal_logs_%s.zip", time.Now().Format("2006-01-02_15-04-05")))
	defer func() {
		if err := os.Remove(zipPath); err != nil {
			h.config.Logger.Warnf("could not remove temp zip %s: %v", zipPath, err)
		}
	}() // Clean up after sending

	err = h.createLogZip(logFiles, zipPath)
	if err != nil {
		h.config.Logger.Errorf("Error creating zip file: %v", err)
		h.sendErrorFollowup(s, i, "❌ Error creating log archive.")
		return
	}

	// Send the zip file
	file, err := os.Open(zipPath)
	if err != nil {
		h.config.Logger.Errorf("Error opening zip file: %v", err)
		h.sendErrorFollowup(s, i, "❌ Error opening log archive.")
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			h.config.Logger.Warnf("error closing zip file: %v", err)
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
		h.config.Logger.Errorf("Error sending log archive: %v", err)
		h.sendErrorFollowup(s, i, "❌ Error sending log archive.")
	}
}

func (h *SlashHandler) handleLogLatest(s *discordgo.Session, i *discordgo.InteractionCreate) {
	logDir := h.config.GetLogDir()
	if logDir == "" {
		h.sendErrorFollowup(s, i, "❌ Log directory is not configured.")
		return
	}

	// Get the latest log file
	latestLogFile, err := h.getLatestLogFile(logDir)
	if err != nil {
		h.config.Logger.Errorf("Error getting latest log file: %v", err)
		h.sendErrorFollowup(s, i, "❌ Error finding latest log file.")
		return
	}

	if latestLogFile == "" {
		h.sendErrorFollowup(s, i, "❌ No log files found.")
		return
	}

	// Read the last 500 lines
	lines, err := h.getLastNLines(latestLogFile, 500)
	if err != nil {
		h.config.Logger.Errorf("Error reading log file: %v", err)
		h.sendErrorFollowup(s, i, "❌ Error reading log file.")
		return
	}

	// Create a temporary file with the content
	tempPath := filepath.Join(os.TempDir(), fmt.Sprintf("gamerpal_latest_%s.txt", time.Now().Format("2006-01-02_15-04-05")))
	defer func() {
		if err := os.Remove(tempPath); err != nil {
			h.config.Logger.Warnf("could not remove temp log file %s: %v", tempPath, err)
		}
	}() // Clean up after sending

	err = os.WriteFile(tempPath, []byte(strings.Join(lines, "\n")), 0644)
	if err != nil {
		h.config.Logger.Errorf("Error creating temp file: %v", err)
		h.sendErrorFollowup(s, i, "❌ Error creating log file.")
		return
	}

	// Send the text file
	file, err := os.Open(tempPath)
	if err != nil {
		h.config.Logger.Errorf("Error opening temp file: %v", err)
		h.sendErrorFollowup(s, i, "❌ Error opening log file.")
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			h.config.Logger.Warnf("error closing temp log file: %v", err)
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
		h.config.Logger.Errorf("Error sending latest log: %v", err)
		h.sendErrorFollowup(s, i, "❌ Error sending log file.")
	}
}

// getLogFiles returns a sorted list of log files in the directory
func (h *SlashHandler) getLogFiles(logDir string) ([]string, error) {
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
func (h *SlashHandler) getLatestLogFile(logDir string) (string, error) {
	logFiles, err := h.getLogFiles(logDir)
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
func (h *SlashHandler) createLogZip(logFiles []string, zipPath string) error {
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := zipFile.Close(); err != nil {
			h.config.Logger.Warnf("error closing zip writer file: %v", err)
		}
	}()

	zipWriter := zip.NewWriter(zipFile)
	defer func() {
		if err := zipWriter.Close(); err != nil {
			h.config.Logger.Warnf("error closing zip writer: %v", err)
		}
	}()

	for _, logFile := range logFiles {
		err := h.addFileToZip(zipWriter, logFile)
		if err != nil {
			return fmt.Errorf("error adding %s to zip: %w", logFile, err)
		}
	}

	return nil
}

// addFileToZip adds a single file to the zip archive
func (h *SlashHandler) addFileToZip(zipWriter *zip.Writer, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			h.config.Logger.Warnf("error closing log file during zip: %v", err)
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
func (h *SlashHandler) getLastNLines(filePath string, n int) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := file.Close(); err != nil {
		h.config.Logger.Warnf("error closing file while tailing: %v", err)
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
func (h *SlashHandler) sendErrorFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr(message),
	})
}
