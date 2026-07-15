package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type PlatformPaths struct {
	ConfigDir         string
	ConfigFile        string
	ModelsDir         string
	StateDir          string
	LogFile           string
	CacheDir          string
	DebugSegmentsDir  string
	SocketPath        string
	LegacyConfigFiles []string
	SwaySocket        string
}

type PathEnvironment struct {
	HomeDir       string
	UserConfigDir string
	UserCacheDir  string
	XDGConfigHome string
	XDGDataHome   string
	XDGStateHome  string
	XDGCacheHome  string
	XDGRuntimeDir string
	TempDir       string
	User          string
	UID           int
	SwaySocket    string
}

func CurrentPlatformPaths() PlatformPaths {
	home, _ := os.UserHomeDir()
	configDir, _ := os.UserConfigDir()
	cacheDir, _ := os.UserCacheDir()
	return PathsFor(runtime.GOOS, PathEnvironment{
		HomeDir:       home,
		UserConfigDir: configDir,
		UserCacheDir:  cacheDir,
		XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
		XDGDataHome:   os.Getenv("XDG_DATA_HOME"),
		XDGStateHome:  os.Getenv("XDG_STATE_HOME"),
		XDGCacheHome:  os.Getenv("XDG_CACHE_HOME"),
		XDGRuntimeDir: os.Getenv("XDG_RUNTIME_DIR"),
		TempDir:       os.TempDir(),
		User:          os.Getenv("USER"),
		UID:           os.Getuid(),
		SwaySocket:    os.Getenv("SWAYSOCK"),
	})
}

// PathsFor resolves paths only from its arguments.
func PathsFor(platform string, env PathEnvironment) PlatformPaths {
	home := env.HomeDir
	legacy := []string{
		filepath.Join(home, ".config", "waydict.toml"),
		filepath.Join(home, ".config", "waydict", "config.toml"),
	}
	if platform == "darwin" {
		configRoot := env.UserConfigDir
		if configRoot == "" {
			configRoot = filepath.Join(home, "Library", "Application Support")
		}
		cacheRoot := env.UserCacheDir
		if cacheRoot == "" {
			cacheRoot = filepath.Join(home, "Library", "Caches")
		}
		appRoot := filepath.Join(configRoot, "Waydict")
		stateDir := filepath.Join(appRoot, "state")
		return PlatformPaths{
			ConfigDir:         appRoot,
			ConfigFile:        filepath.Join(appRoot, "config.toml"),
			ModelsDir:         filepath.Join(appRoot, "models"),
			StateDir:          stateDir,
			LogFile:           filepath.Join(home, "Library", "Logs", "Waydict", "waydict.log"),
			CacheDir:          filepath.Join(cacheRoot, "Waydict"),
			DebugSegmentsDir:  filepath.Join(stateDir, "segments"),
			SocketPath:        filepath.Join("/tmp", fmt.Sprintf("waydict-%d", env.UID), "control.sock"),
			LegacyConfigFiles: legacy,
		}
	}

	configRoot := env.XDGConfigHome
	if configRoot == "" {
		configRoot = filepath.Join(home, ".config")
	}
	dataRoot := env.XDGDataHome
	if dataRoot == "" {
		dataRoot = filepath.Join(home, ".local", "share")
	}
	stateRoot := env.XDGStateHome
	if stateRoot == "" {
		stateRoot = filepath.Join(home, ".local", "state")
	}
	cacheRoot := env.XDGCacheHome
	if cacheRoot == "" {
		cacheRoot = filepath.Join(home, ".cache")
	}
	runtimeRoot := env.XDGRuntimeDir
	if runtimeRoot == "" {
		user := env.User
		if user == "" {
			user = "user"
		}
		tempDir := env.TempDir
		if tempDir == "" {
			tempDir = "/tmp"
		}
		runtimeRoot = filepath.Join(tempDir, "waydict-"+user)
	}
	stateDir := filepath.Join(stateRoot, "waydict")
	return PlatformPaths{
		ConfigDir:        filepath.Join(configRoot, "waydict"),
		ConfigFile:       filepath.Join(configRoot, "waydict.toml"),
		ModelsDir:        filepath.Join(dataRoot, "waydict", "models"),
		StateDir:         stateDir,
		LogFile:          filepath.Join(stateDir, "waydict.log"),
		CacheDir:         filepath.Join(cacheRoot, "waydict"),
		DebugSegmentsDir: filepath.Join(stateDir, "segments"),
		SocketPath:       filepath.Join(runtimeRoot, "waydict", "waydict.sock"),
		LegacyConfigFiles: []string{
			filepath.Join(configRoot, "waydict.toml"),
			filepath.Join(configRoot, "waydict", "config.toml"),
		},
		SwaySocket: env.SwaySocket,
	}
}

func ConfigSearchPathsFor(platform string, paths PlatformPaths, override string) []string {
	if override != "" {
		return []string{override}
	}
	if platform == "darwin" {
		out := []string{paths.ConfigFile}
		return append(out, paths.LegacyConfigFiles...)
	}
	if len(paths.LegacyConfigFiles) != 0 {
		return append([]string(nil), paths.LegacyConfigFiles...)
	}
	return []string{paths.ConfigFile}
}

func (p PlatformPaths) SileroModelPath() string {
	return filepath.Join(p.ModelsDir, "silero_vad.onnx")
}

func (p PlatformPaths) ParakeetModelPath() string {
	return filepath.Join(p.ModelsDir, DefaultModelName)
}
