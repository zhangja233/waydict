package hotkey

import (
	"fmt"
	"strings"
)

const SupportedModifiers = ModifierControl | ModifierShift | ModifierOption | ModifierCommand

var symbolicKeyCodes = map[string]uint16{
	"a": 0, "b": 11, "c": 8, "d": 2, "e": 14, "f": 3, "g": 5,
	"h": 4, "i": 34, "j": 38, "k": 40, "l": 37, "m": 46, "n": 45,
	"o": 31, "p": 35, "q": 12, "r": 15, "s": 1, "t": 17, "u": 32,
	"v": 9, "w": 13, "x": 7, "y": 16, "z": 6,
	"0": 29, "1": 18, "2": 19, "3": 20, "4": 21,
	"5": 23, "6": 22, "7": 26, "8": 28, "9": 25,
	"space": 49, "return": 36, "tab": 48, "escape": 53,
	"left": 123, "right": 124, "down": 125, "up": 126,
	"f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96,
	"f6": 97, "f7": 98, "f8": 100, "f9": 101, "f10": 109,
	"f11": 103, "f12": 111, "f13": 105, "f14": 107, "f15": 113,
	"f16": 106, "f17": 64, "f18": 79, "f19": 80, "f20": 90,
}

var symbolicKeyNames = func() map[uint16]string {
	names := make(map[uint16]string, len(symbolicKeyCodes))
	for name, code := range symbolicKeyCodes {
		names[code] = name
	}
	return names
}()

func ResolveBinding(key string, keyCode int, modifierNames []string, mode Mode) (Binding, error) {
	if keyCode < -1 || keyCode > 65535 {
		return Binding{}, fmt.Errorf("hotkey.key_code must be between -1 and 65535")
	}
	modifiers, err := ParseModifiers(modifierNames)
	if err != nil {
		return Binding{}, err
	}
	if !validMode(mode) {
		return Binding{}, fmt.Errorf("unsupported hotkey mode %q", mode)
	}
	if keyCode >= 0 {
		code := uint16(keyCode)
		return Binding{Key: DisplayKeyCode(code), KeyCode: code, Modifiers: modifiers, Mode: mode}, nil
	}
	name := strings.ToLower(strings.TrimSpace(key))
	if name == "" {
		return Binding{}, fmt.Errorf("hotkey.key must not be empty when key_code is unset")
	}
	code, ok := symbolicKeyCodes[name]
	if !ok {
		return Binding{}, fmt.Errorf("unsupported symbolic hotkey key %q", key)
	}
	return Binding{Key: name, KeyCode: code, Modifiers: modifiers, Mode: mode}, nil
}

func ValidateBinding(binding Binding) error {
	if !validMode(binding.Mode) {
		return fmt.Errorf("unsupported hotkey mode %q", binding.Mode)
	}
	if binding.Modifiers&^SupportedModifiers != 0 {
		return fmt.Errorf("hotkey binding contains unsupported modifier bits")
	}
	return nil
}

func DisplayKeyCode(keyCode uint16) string {
	if name, ok := symbolicKeyNames[keyCode]; ok {
		return name
	}
	return fmt.Sprintf("keycode:%d", keyCode)
}

func NormalizeModifiers(modifiers Modifiers) Modifiers {
	return modifiers & SupportedModifiers
}

func Matches(binding Binding, keyCode uint16, modifiers Modifiers, autorepeat bool) bool {
	return !autorepeat && keyCode == binding.KeyCode && NormalizeModifiers(modifiers) == binding.Modifiers
}

func validMode(mode Mode) bool {
	return mode == ModeHold || mode == ModeToggle || mode == ModeOneshot
}
