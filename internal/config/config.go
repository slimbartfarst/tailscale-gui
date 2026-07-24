// internal/config/config.go
//
// User-editable settings stored at ~/.config/tailscale-gui/config.json.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	appDirName     = "tailscale-gui"
	configFileName = "config.json"
)

// Config holds all user-configurable settings.
type Config struct {
	// AdminConsoleURL is opened by "Admin console…".
	// Set to your Headscale web UI if self-hosting.
	AdminConsoleURL string `json:"admin_console_url"`

	// NotificationsEnabled controls desktop notifications via notify-send.
	NotificationsEnabled bool `json:"notifications_enabled"`

	// TaildropDir is where received files are saved.
	// Defaults to ~/Downloads/Taildrop.
	TaildropDir string `json:"taildrop_dir"`

	// StartMinimised launches without opening any window.
	StartMinimised bool `json:"start_minimised"`

	// SocketPath overrides the tailscaled socket (empty = platform default).
	SocketPath string `json:"socket_path,omitempty"`

	// PollIntervalSec controls how often peers are refreshed (default 30).
	PollIntervalSec int `json:"poll_interval_sec"`

	// StatusWindowPort is the localhost port for the HTML status window.
	// 0 means pick a random free port.
	StatusWindowPort int `json:"status_window_port"`

	// TerminalCmd overrides the auto-detected terminal emulator.
	// Include the exec flag, e.g. "alacritty -e" or "xterm -e".
	// Empty = auto-detect from $TERMINAL, desktop environment, then fallback list.
	TerminalCmd string `json:"terminal_cmd,omitempty"`

	// SSHUser is the username for SSH connections.
	// Empty = let ssh decide (uses ~/.ssh/config or $USER).
	SSHUser string `json:"ssh_user,omitempty"`
}

// Default returns a Config with sensible out-of-box values.
func Default() *Config {
	return &Config{
		AdminConsoleURL:      "https://login.tailscale.com/admin/machines",
		NotificationsEnabled: true,
		TaildropDir:          "~/Downloads/Taildrop",
		StartMinimised:       true,
		PollIntervalSec:      30,
		StatusWindowPort:     0,
	}
}

// Load reads the config file, creating it with defaults on first run.
func Load() (*Config, error) {
	path, err := configFilePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := Default()
		_ = cfg.Save() // best-effort; ignore error
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Start from defaults so fields added in new versions have values.
	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

// Save writes the config to disk atomically.
func (c *Config) Save() error {
	path, err := configFilePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// Write to a temp file then rename for atomicity.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return os.Rename(tmp, path)
}

// Dir returns the config directory, creating it if needed.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, appDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return dir, nil
}

func configFilePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}
