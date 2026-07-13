package inject

import (
	"testing"

	"waydict/internal/config"
)

func TestPostProcessorPunctuationSpacing(t *testing.T) {
	cfg := config.Defaults()
	p := NewPostProcessor(cfg.PostProcess, true, nil)
	got, next := p.Apply(" Hello   ( world ) , test !", CaseState{AtBoundary: true})
	if got != "Hello (world), test! " {
		t.Fatalf("got %q", got)
	}
	if !next.AtBoundary {
		t.Fatal("terminal punctuation did not set the boundary")
	}
}

func TestSpokenCommands(t *testing.T) {
	cfg := config.Defaults()
	cfg.PostProcess.SpokenFormattingCommands = true
	p := NewPostProcessor(cfg.PostProcess, true, nil)
	tests := []struct {
		name string
		text string
		st   CaseState
		want string
		next CaseState
	}{
		{name: "new line", text: "new line", st: CaseState{}, want: "\n", next: CaseState{AtBoundary: true}},
		{name: "new paragraph", text: "new paragraph", st: CaseState{}, want: "\n\n", next: CaseState{AtBoundary: true}},
		{name: "tab", text: "tab", st: CaseState{}, want: "\t", next: CaseState{}},
		{name: "scratch", text: "scratch that", st: CaseState{AtBoundary: true}, want: "", next: CaseState{AtBoundary: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, next := p.Apply(tc.text, tc.st)
			if got != tc.want || next != tc.next {
				t.Fatalf("Apply() = %q, %+v; want %q, %+v", got, next, tc.want, tc.next)
			}
		})
	}
}

func TestPostProcessorSmartCase(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		st         CaseState
		vocabulary []string
		want       string
		next       CaseState
	}{
		{
			name: "complete sentence at boundary",
			text: "hello there.",
			st:   CaseState{AtBoundary: true},
			want: "Hello there. ",
			next: CaseState{AtBoundary: true},
		},
		{
			name: "fragment at boundary",
			text: "Search term",
			st:   CaseState{AtBoundary: true},
			want: "search term ",
			next: CaseState{},
		},
		{
			name: "complete continuation",
			text: "Jumped over.",
			st:   CaseState{},
			want: "jumped over. ",
			next: CaseState{AtBoundary: true},
		},
		{
			name: "standalone I and contractions",
			text: "i think i'm right and i'll try.",
			st:   CaseState{AtBoundary: true},
			want: "I think I'm right and I'll try. ",
			next: CaseState{AtBoundary: true},
		},
		{
			name: "acronym",
			text: "NASA data",
			st:   CaseState{},
			want: "NASA data ",
			next: CaseState{},
		},
		{
			name:       "protected word",
			text:       "Claude helped",
			st:         CaseState{},
			vocabulary: []string{"Claude"},
			want:       "Claude helped ",
			next:       CaseState{},
		},
		{
			name:       "protected possessive",
			text:       "Claude's answer",
			st:         CaseState{},
			vocabulary: []string{"Claude"},
			want:       "Claude's answer ",
			next:       CaseState{},
		},
		{
			name: "acronym possessive",
			text: "NASA’s launch",
			st:   CaseState{},
			want: "NASA’s launch ",
			next: CaseState{},
		},
		{
			name: "closing punctuation",
			text: `hello there.")`,
			st:   CaseState{AtBoundary: true},
			want: `Hello there.") `,
			next: CaseState{AtBoundary: true},
		},
		{
			name: "unicode closing quote",
			text: "hello there.”",
			st:   CaseState{AtBoundary: true},
			want: "Hello there.” ",
			next: CaseState{AtBoundary: true},
		},
		{
			name: "unicode closing apostrophe",
			text: "hello there.’",
			st:   CaseState{AtBoundary: true},
			want: "Hello there.’ ",
			next: CaseState{AtBoundary: true},
		},
		{
			name: "digit led token",
			text: "42nd.",
			st:   CaseState{AtBoundary: true},
			want: "42nd. ",
			next: CaseState{AtBoundary: true},
		},
		{
			name: "unicode first letter",
			text: "élan.",
			st:   CaseState{AtBoundary: true},
			want: "Élan. ",
			next: CaseState{AtBoundary: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			p := NewPostProcessor(cfg.PostProcess, true, tc.vocabulary)
			got, next := p.Apply(tc.text, tc.st)
			if got != tc.want || next != tc.next {
				t.Fatalf("Apply() = %q, %+v; want %q, %+v", got, next, tc.want, tc.next)
			}
		})
	}
}

func TestPostProcessorSmartCaseDisabled(t *testing.T) {
	cfg := config.Defaults()
	cfg.PostProcess.SmartCase = false
	p := NewPostProcessor(cfg.PostProcess, true, nil)
	st := CaseState{}
	got, next := p.Apply("HELLO i.", st)
	if got != "HELLO i. " || next != st {
		t.Fatalf("Apply() = %q, %+v; want unchanged casing and state", got, next)
	}
}

func TestPostProcessorEmptyPreservesState(t *testing.T) {
	cfg := config.Defaults()
	p := NewPostProcessor(cfg.PostProcess, true, nil)
	st := CaseState{AtBoundary: true}
	got, next := p.Apply(" \t\n", st)
	if got != "" || next != st {
		t.Fatalf("Apply() = %q, %+v; want empty output and %+v", got, next, st)
	}
}

func TestPostProcessorReplacements(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		replacements map[string]string
		vocabulary   []string
		st           CaseState
		want         string
	}{
		{
			name:         "whole word case insensitive",
			text:         "the CLOUD",
			replacements: map[string]string{"cloud": "Claude"},
			st:           CaseState{AtBoundary: true},
			want:         "the Claude",
		},
		{
			name:         "substring unchanged",
			text:         "icloud",
			replacements: map[string]string{"cloud": "Claude"},
			st:           CaseState{AtBoundary: true},
			want:         "icloud",
		},
		{
			name:         "replacement target auto-protected from casing",
			text:         "Cloud helped",
			replacements: map[string]string{"cloud": "Claude"},
			st:           CaseState{},
			want:         "Claude helped",
		},
		{
			name:         "literal target",
			text:         "token",
			replacements: map[string]string{"token": "$1"},
			st:           CaseState{},
			want:         "$1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.PostProcess.Replacements = tc.replacements
			p := NewPostProcessor(cfg.PostProcess, false, tc.vocabulary)
			got, _ := p.Apply(tc.text, tc.st)
			if got != tc.want {
				t.Fatalf("Apply() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPostProcessorReplacementOrdering(t *testing.T) {
	cfg := config.Defaults()
	cfg.PostProcess.SmartCase = false
	cfg.PostProcess.Replacements = map[string]string{
		"new":      "old",
		"new york": "NYC",
		"cat":      "dog",
		"dog":      "eel",
	}
	for i := 0; i < 100; i++ {
		p := NewPostProcessor(cfg.PostProcess, false, nil)
		got, _ := p.Apply("new york and new cat", CaseState{})
		if got != "NYC and old dog" {
			t.Fatalf("Apply() = %q, want deterministic single-pass output", got)
		}
	}
}
