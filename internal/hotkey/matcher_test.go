package hotkey

import (
	"strings"
	"testing"
)

func TestSymbolicKeyMap(t *testing.T) {
	want := []string{
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m",
		"n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z",
		"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
		"space", "return", "tab", "escape", "left", "right", "down", "up",
		"f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10",
		"f11", "f12", "f13", "f14", "f15", "f16", "f17", "f18", "f19", "f20",
	}
	seen := make(map[uint16]string, len(want))
	for _, name := range want {
		binding, err := ResolveBinding(name, -1, nil, ModeHold)
		if err != nil {
			t.Fatalf("ResolveBinding(%q): %v", name, err)
		}
		if previous := seen[binding.KeyCode]; previous != "" {
			t.Fatalf("%q and %q share key code %d", previous, name, binding.KeyCode)
		}
		seen[binding.KeyCode] = name
		if got := DisplayKeyCode(binding.KeyCode); got != name {
			t.Fatalf("DisplayKeyCode(%d) = %q, want %q", binding.KeyCode, got, name)
		}
	}
	if len(symbolicKeyCodes) != len(want) {
		t.Fatalf("symbolic key count = %d, want %d", len(symbolicKeyCodes), len(want))
	}
}

func TestMatchesExactChord(t *testing.T) {
	binding, err := ResolveBinding("space", -1, []string{"control", "shift", "command"}, ModeHold)
	if err != nil {
		t.Fatal(err)
	}
	chord := ModifierControl | ModifierShift | ModifierCommand
	tests := []struct {
		name       string
		keyCode    uint16
		modifiers  Modifiers
		autorepeat bool
		want       bool
	}{
		{name: "exact", keyCode: 49, modifiers: chord, want: true},
		{name: "ignored nonbinding flags", keyCode: 49, modifiers: chord | 1<<20, want: true},
		{name: "modifier subset", keyCode: 49, modifiers: ModifierControl | ModifierShift},
		{name: "modifier superset", keyCode: 49, modifiers: chord | ModifierOption},
		{name: "other key", keyCode: 36, modifiers: chord},
		{name: "autorepeat", keyCode: 49, modifiers: chord, autorepeat: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Matches(binding, tc.keyCode, tc.modifiers, tc.autorepeat); got != tc.want {
				t.Fatalf("Matches() = %t, want %t", got, tc.want)
			}
		})
	}
	for modifiers := Modifiers(0); modifiers <= SupportedModifiers; modifiers++ {
		want := modifiers == chord
		if got := Matches(binding, binding.KeyCode, modifiers, false); got != want {
			t.Fatalf("Matches(modifiers=%04b) = %t, want %t", modifiers, got, want)
		}
	}
}

func TestKeyCodeOverridePrecedenceAndDisplay(t *testing.T) {
	binding, err := ResolveBinding("space", 36, []string{"command"}, ModeOneshot)
	if err != nil {
		t.Fatal(err)
	}
	if binding.KeyCode != 36 || binding.Key != "return" {
		t.Fatalf("known override = %#v", binding)
	}
	binding, err = ResolveBinding("not-a-key", 65535, nil, ModeToggle)
	if err != nil {
		t.Fatal(err)
	}
	if binding.KeyCode != 65535 || binding.Key != "keycode:65535" {
		t.Fatalf("unknown override = %#v", binding)
	}
}

func TestResolveBindingValidation(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		keyCode   int
		modifiers []string
		mode      Mode
		contains  string
	}{
		{name: "key code below range", key: "space", keyCode: -2, mode: ModeHold, contains: "between -1 and 65535"},
		{name: "key code above range", key: "space", keyCode: 65536, mode: ModeHold, contains: "between -1 and 65535"},
		{name: "empty symbolic key", keyCode: -1, mode: ModeHold, contains: "must not be empty"},
		{name: "unknown symbolic key", key: "delete", keyCode: -1, mode: ModeHold, contains: "unsupported symbolic"},
		{name: "duplicate modifier", key: "space", keyCode: -1, modifiers: []string{"shift", "shift"}, mode: ModeHold, contains: "duplicate"},
		{name: "unsupported modifier", key: "space", keyCode: -1, modifiers: []string{"meta"}, mode: ModeHold, contains: "unsupported"},
		{name: "unsupported mode", key: "space", keyCode: -1, mode: "sticky", contains: "unsupported hotkey mode"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveBinding(tc.key, tc.keyCode, tc.modifiers, tc.mode)
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("ResolveBinding() error = %v, want containing %q", err, tc.contains)
			}
		})
	}
}

func TestValidateBindingRejectsUnsupportedModifierBits(t *testing.T) {
	err := ValidateBinding(Binding{Key: "space", KeyCode: 49, Modifiers: ModifierCommand | 1<<12, Mode: ModeHold})
	if err == nil {
		t.Fatal("ValidateBinding accepted unsupported modifier bits")
	}
}
