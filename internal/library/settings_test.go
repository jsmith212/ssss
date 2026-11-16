package library

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ssss/internal/model"
)

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")

	cfg, err := LoadConfig(path)
	if err != nil || !reflect.DeepEqual(cfg, DefaultConfig()) {
		t.Fatalf("missing file: %+v, %v", cfg, err)
	}
	cfg.LibraryPath, cfg.LibraryID, cfg.LibraryName = "/music/Samples", "lib_X", "Main"
	cfg.Settings.Theme = "dark"
	cfg.Settings.Volume = 0.5
	cfg.Settings.PinnedTags = []string{"Drums/Kick", "", "bass"}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	if left := tempFiles(t, filepath.Dir(path)); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Settings.PinnedTags = []string{"drums/kick", "", "bass"}
	if !reflect.DeepEqual(got, cfg) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, cfg)
	}
}

func TestConfigDefaultsAndUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	// An older or hand-edited file: missing members, junk values, extras.
	os.WriteFile(path, []byte(`{"v":1,"libraryPath":"/x","deviceId":"dev1",
		"settings":{"theme":"neon","volume":7,"projectBpm":-3,"sidebarWidth":5000,"matchKey":true,"pinnedTags":null}}`), 0o644)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	want := model.DefaultSettings()
	want.Volume, want.SidebarWidth, want.MatchKey = 1, 320, true
	if !reflect.DeepEqual(cfg.Settings, want) || cfg.LibraryPath != "/x" {
		t.Errorf("settings = %+v\nwant %+v", cfg.Settings, want)
	}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), `"deviceId": "dev1"`) {
		t.Errorf("unknown member lost: %s", data)
	}

	os.WriteFile(path, []byte(`{broken`), 0o644)
	cfg, err = LoadConfig(path)
	if err == nil || !reflect.DeepEqual(cfg, DefaultConfig()) {
		t.Errorf("corrupt file: %+v, %v", cfg, err)
	}
}

func TestConfigAndCachePaths(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvConfigDir, filepath.Join(dir, "cfg"))
	t.Setenv(EnvCacheDir, filepath.Join(dir, "cache"))
	if got := must(ConfigPath()); got != filepath.Join(dir, "cfg", "settings.json") {
		t.Errorf("config path = %q", got)
	}
	if got := must(CacheDir("lib_01ABC")); got != filepath.Join(dir, "cache", "lib_01ABC") {
		t.Errorf("cache dir = %q", got)
	}
	for _, bad := range []string{"", "../x", "a/b"} {
		if _, err := CacheDir(bad); err == nil {
			t.Errorf("CacheDir(%q) accepted", bad)
		}
	}

	t.Setenv(EnvConfigDir, "")
	t.Setenv(EnvCacheDir, "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	userCfg, err := os.UserConfigDir()
	if err != nil {
		t.Skip(err)
	}
	if got := must(ConfigPath()); got != filepath.Join(userCfg, "ssss", "settings.json") {
		t.Errorf("default config path = %q", got)
	}
	userCache := must(os.UserCacheDir())
	if got := must(CacheDir("lib_1")); got != filepath.Join(userCache, "ssss", "lib_1") {
		t.Errorf("default cache dir = %q", got)
	}
}
