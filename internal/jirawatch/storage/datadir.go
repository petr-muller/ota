package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	// dataDirName is the subdirectory within the user's data directory where query files are stored
	dataDirName = "jira-queries"
)

// JiraWatchDataDir returns the data directory path for jira-watch storage
func JiraWatchDataDir() (string, error) {
	var dataDir string
	
	// Try XDG_DATA_HOME first, then fallback to ~/.local/share
	if xdgDataHome := os.Getenv("XDG_DATA_HOME"); xdgDataHome != "" {
		dataDir = xdgDataHome
	} else {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot obtain user home dir: %w", err)
		}
		dataDir = filepath.Join(homeDir, ".local", "share")
	}

	jiraWatchDataDir := filepath.Join(dataDir, "ota", dataDirName)
	return jiraWatchDataDir, nil
}