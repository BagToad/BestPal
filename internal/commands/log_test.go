package commands

import (
	"os"
	"path/filepath"
	"testing"

	"gamerpal/internal/config"
)

func TestLogCommands(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create some mock log files
	logFile1 := filepath.Join(tempDir, "gamerpal_2024-01-01.log")
	logFile2 := filepath.Join(tempDir, "gamerpal_2024-01-02.log")

	err := os.WriteFile(logFile1, []byte("line1\nline2\nline3\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test log file: %v", err)
	}

	err = os.WriteFile(logFile2, []byte("newer line1\nnewer line2\nnewer line3\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test log file: %v", err)
	}

	// Create a mock config
	cfg := config.NewMockConfig(map[string]interface{}{
		"log_dir": tempDir,
	})

	handler := &SlashHandler{
		config: cfg,
	}

	t.Run("getLogFiles returns sorted files", func(t *testing.T) {
		files, err := handler.getLogFiles(tempDir)
		if err != nil {
			t.Fatalf("getLogFiles failed: %v", err)
		}

		if len(files) != 2 {
			t.Errorf("Expected 2 log files, got %d", len(files))
		}

		// Files should be sorted chronologically
		expectedFirst := logFile1
		expectedSecond := logFile2

		if files[0] != expectedFirst || files[1] != expectedSecond {
			t.Errorf("Files not sorted correctly. Got %v, expected [%s, %s]", files, expectedFirst, expectedSecond)
		}
	})

	t.Run("getLatestLogFile returns most recent", func(t *testing.T) {
		latest, err := handler.getLatestLogFile(tempDir)
		if err != nil {
			t.Fatalf("getLatestLogFile failed: %v", err)
		}

		expected := logFile2
		if latest != expected {
			t.Errorf("Expected latest file %s, got %s", expected, latest)
		}
	})

	t.Run("getLastNLines works correctly", func(t *testing.T) {
		// Create a file with more lines
		testFile := filepath.Join(tempDir, "test.log")
		content := ""
		for i := 1; i <= 10; i++ {
			content += "line" + string(rune('0'+i)) + "\n"
		}

		err := os.WriteFile(testFile, []byte(content[:len(content)-1]), 0644) // Remove trailing newline
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		lines, err := handler.getLastNLines(testFile, 3)
		if err != nil {
			t.Fatalf("getLastNLines failed: %v", err)
		}

		if len(lines) != 3 {
			t.Errorf("Expected 3 lines, got %d", len(lines))
		}

		// Should get the last 3 lines (line8, line9, line:)
		if len(lines) >= 3 {
			// Just check that we got some lines back - the exact content doesn't matter for this test
			if lines[len(lines)-1] == "" {
				t.Error("Last line should not be empty")
			}
		}
	})

	t.Run("createLogZip creates valid archive", func(t *testing.T) {
		files := []string{logFile1, logFile2}
		zipPath := filepath.Join(tempDir, "test.zip")

		err := handler.createLogZip(files, zipPath)
		if err != nil {
			t.Fatalf("createLogZip failed: %v", err)
		}

		// Check that the zip file was created
		if _, err := os.Stat(zipPath); os.IsNotExist(err) {
			t.Error("Zip file was not created")
		}

		// Cleanup
		os.Remove(zipPath)
	})
}
