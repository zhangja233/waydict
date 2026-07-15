package preferences

import (
	"context"
	"fmt"

	"waydict/internal/config"
)

type Source string

const (
	SourceConfig     Source = "config"
	SourcePreference Source = "preference"
	SourceDefault    Source = "default"
)

type Value struct {
	Value  string
	Source Source
}

func AudioDevice(ctx context.Context, cfg config.Config, store Store, valid func(string) bool) (Value, string, error) {
	if cfg.IsExplicit("audio.device") || cfg.Audio.Device != "" {
		return Value{Value: cfg.Audio.Device, Source: SourceConfig}, "", nil
	}
	return resolveString(ctx, store, KeySelectedAudioDeviceUID, cfg.Audio.Device, valid)
}

func HotkeyMode(ctx context.Context, cfg config.Config, store Store) (Value, string, error) {
	if cfg.IsExplicit("hotkey.mode") {
		return Value{Value: cfg.Hotkey.Mode, Source: SourceConfig}, "", nil
	}
	return resolveString(ctx, store, KeySelectedHotkeyMode, cfg.Hotkey.Mode, func(value string) bool {
		return value == "hold" || value == "toggle" || value == "oneshot"
	})
}

func resolveString(ctx context.Context, store Store, key, fallback string, valid func(string) bool) (Value, string, error) {
	if store == nil {
		return Value{Value: fallback, Source: SourceDefault}, "", nil
	}
	value, ok, err := store.String(ctx, key)
	if err != nil {
		return Value{}, "", err
	}
	if !ok || value == "" {
		return Value{Value: fallback, Source: SourceDefault}, "", nil
	}
	if valid != nil && !valid(value) {
		if err := store.Delete(ctx, key); err != nil {
			return Value{}, "", err
		}
		return Value{Value: fallback, Source: SourceDefault}, fmt.Sprintf("cleared invalid %s preference", key), nil
	}
	return Value{Value: value, Source: SourcePreference}, "", nil
}
