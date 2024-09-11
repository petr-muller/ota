package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	// configDirName is a directory in the user's config directory where OTA configuration is stored
	configDirName string = "ota"
)

func MustOtaConfigDir() string {
	configDig, err := os.UserConfigDir()
	if err != nil {
		panic(fmt.Errorf("cannot obtain user config dir: %w", err))
	}

	otaConfigDir := filepath.Join(configDig, configDirName)
	return otaConfigDir
}
