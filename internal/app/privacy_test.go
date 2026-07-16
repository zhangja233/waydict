package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"waydict/internal/config"
	"waydict/internal/control"
	svlog "waydict/internal/log"
)

func TestMacOSStatusAndDefaultLogsExcludeTranscriptText(t *testing.T) {
	const transcript = "PR7-TRANSCRIPT-MUST-NOT-APPEAR"
	var output bytes.Buffer
	cfg := config.DefaultsFor("darwin", config.PlatformPaths{})
	cfg.Daemon.RedactTranscriptsInLogs = false
	cfg.Focus.Enabled = false
	injector := &MemoryInjector{}
	injector.SetError(errors.New("native injection failed: " + transcript))
	application := New(context.Background(), cfg, Dependencies{
		Capabilities: ControlCapabilities{Platform: "darwin", Host: "macos_app"},
		Logger:       svlog.New("debug", &output),
		Injector:     injector,
	})
	application.recordTranscript(transcript)
	application.recordUninjected(transcript, errors.New("injection rejected: "+transcript))
	application.mu.Lock()
	application.status.Focus.StableID = transcript
	application.status.Focus.AppName = transcript
	application.status.Focus.FocusedName = transcript
	application.mu.Unlock()
	response := application.HandleControl(context.Background(), control.NewRequest("inject_text", map[string]any{"text": transcript}))

	status := application.Status(context.Background())
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"status": string(encoded), "control": string(responseJSON), "log": output.String()} {
		if strings.Contains(value, transcript) {
			t.Fatalf("transcript reached %s: %s", name, value)
		}
	}
	if !status.LastTranscriptRedacted || status.LastTranscript != "" || status.LastUninjectedText != "" || status.Focus.StableID != "" || status.Focus.AppName != "" || status.Focus.FocusedName != "" {
		t.Fatalf("macOS status was not sanitized: %+v", status)
	}
}
