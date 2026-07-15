package preferences

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"waydict/internal/config"
)

func TestPreferencePrecedence(t *testing.T) {
	ctx := context.Background()
	paths := config.PathsFor("darwin", config.PathEnvironment{
		HomeDir:       t.TempDir(),
		UserConfigDir: filepath.Join(t.TempDir(), "Application Support"),
		UserCacheDir:  t.TempDir(),
		TempDir:       "/tmp",
		UID:           501,
	})
	cfg := config.DefaultsFor("darwin", paths)
	store := NewMemoryStore()
	store.Values[KeySelectedAudioDeviceUID] = "device-1"
	value, warning, err := AudioDevice(ctx, cfg, store, func(string) bool { return true })
	if err != nil || warning != "" || value.Value != "device-1" || value.Source != SourcePreference {
		t.Fatalf("preference resolution = %+v warning=%q err=%v", value, warning, err)
	}

	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[audio]\ndevice = \"configured\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadFor("darwin", paths, configPath)
	if err != nil {
		t.Fatal(err)
	}
	value, _, err = AudioDevice(ctx, cfg, store, func(string) bool { return true })
	if err != nil || value.Value != "configured" || value.Source != SourceConfig {
		t.Fatalf("config resolution = %+v err=%v", value, err)
	}
}

func TestInvalidPreferenceIsCleared(t *testing.T) {
	cfg := config.DefaultsFor("darwin", config.PlatformPaths{})
	store := NewMemoryStore()
	store.Values[KeySelectedHotkeyMode] = "invalid"
	value, warning, err := HotkeyMode(context.Background(), cfg, store)
	if err != nil || warning == "" || value.Value != "hold" || value.Source != SourceDefault {
		t.Fatalf("resolution = %+v warning=%q err=%v", value, warning, err)
	}
	if _, ok := store.Values[KeySelectedHotkeyMode]; ok {
		t.Fatal("invalid preference was not cleared")
	}
}
