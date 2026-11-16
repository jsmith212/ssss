package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"

	"ssss/internal/model"
)

const appDirName = "ssss"

// Environment overrides for the per-machine folders, so tests and headless
// runs never touch the user's real configuration or cache.
const (
	EnvConfigDir = "SSSS_CONFIG_DIR"
	EnvCacheDir  = "SSSS_CACHE_DIR"
)

const maxPinnedTags = 9

// Config is the per-machine settings file. It is never synced: it remembers
// where this machine keeps the library and the UI settings.
type Config struct {
	V int `json:"v"`
	// LibraryPath is the library root; "" until the user picks one.
	LibraryPath string `json:"libraryPath"`
	// LibraryID and LibraryName let the app find its cache and label the
	// library while the folder is unavailable.
	LibraryID   string         `json:"libraryId"`
	LibraryName string         `json:"libraryName"`
	Settings    model.Settings `json:"settings"`

	extra extras
}

type configJSON Config

func (c *Config) UnmarshalJSON(b []byte) error {
	a := configJSON(*c) // keep defaults for members the file lacks
	ex, err := decodeExtras(b, &a)
	if err != nil {
		return err
	}
	*c = Config(a)
	c.extra = ex
	return nil
}

func (c Config) MarshalJSON() ([]byte, error) {
	return encodeExtras(configJSON(c), c.extra)
}

// DefaultConfig is what a first run starts from.
func DefaultConfig() Config {
	return Config{V: FormatVersion, Settings: model.DefaultSettings()}
}

// ConfigPath is os.UserConfigDir()/ssss/settings.json.
func ConfigPath() (string, error) {
	dir := os.Getenv(EnvConfigDir)
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("locate settings folder: %w", err)
		}
		dir = filepath.Join(base, appDirName)
	}
	return filepath.Join(dir, "settings.json"), nil
}

// CacheDir is os.UserCacheDir()/ssss/<libraryID>, home of index.db and the
// peaks folder. It is not created.
func CacheDir(libraryID string) (string, error) {
	if !isSafeID(libraryID) {
		return "", fmt.Errorf("%w: library id %q", ErrInvalid, libraryID)
	}
	dir := os.Getenv(EnvCacheDir)
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("locate cache folder: %w", err)
		}
		dir = filepath.Join(base, appDirName)
	}
	return filepath.Join(dir, libraryID), nil
}

// LoadConfig reads the settings file at path. A missing file yields the
// defaults and no error; an unreadable or corrupt one yields the defaults
// together with the error, so the app can start and say what happened.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read settings: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultConfig(), fmt.Errorf("read settings: %w", err)
	}
	cfg.Settings = NormalizeSettings(cfg.Settings)
	return cfg, nil
}

// SaveConfig writes the settings file atomically, creating its folder.
func SaveConfig(path string, cfg Config) error {
	cfg.V = FormatVersion
	cfg.Settings = NormalizeSettings(cfg.Settings)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return writeFileAtomic(path, append(data, '\n'))
}

// NormalizeSettings replaces out-of-range or unknown values with defaults,
// so a hand-edited file or a buggy caller cannot wedge the UI.
func NormalizeSettings(s model.Settings) model.Settings {
	def := model.DefaultSettings()
	switch s.Theme {
	case "system", "light", "dark":
	default:
		s.Theme = def.Theme
	}
	switch s.Density {
	case "compact", "regular":
	default:
		s.Density = def.Density
	}
	if math.IsNaN(s.Volume) {
		s.Volume = def.Volume
	}
	s.Volume = min(1, max(0, s.Volume))
	if math.IsNaN(s.ProjectBPM) || s.ProjectBPM < 20 || s.ProjectBPM > 999 {
		s.ProjectBPM = def.ProjectBPM
	}
	if s.SidebarWidth == 0 {
		s.SidebarWidth = def.SidebarWidth
	}
	s.SidebarWidth = min(320, max(180, s.SidebarWidth))
	// Pinned tags are positional (slots 1..9), so empty slots are kept.
	pinned := make([]string, 0, maxPinnedTags)
	for i, tag := range s.PinnedTags {
		if i == maxPinnedTags {
			break
		}
		pinned = append(pinned, NormalizeTag(tag))
	}
	s.PinnedTags = pinned
	return s
}
