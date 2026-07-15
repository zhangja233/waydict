package api

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

func TestStatusJSONGolden(t *testing.T) {
	mode := ModeHold
	tests := []struct {
		name   string
		status Status
	}{
		{
			name: "populated",
			status: Status{
				State: StateRecognizing,
				Mode:  &mode,
				Audio: AudioStatus{
					Backend:    "pipewire",
					SampleRate: 16000,
					LevelDBFS:  -18.75,
					Overruns:   7,
					Capturing:  true,
				},
				VAD: VADStatus{Engine: "silero"},
				ASR: ASRStatus{
					Engine:           "auto",
					Model:            "parakeet-tdt-0.6b-v3-int8",
					Provider:         "cpu",
					ResolvedEngine:   "sherpa-onnx",
					ResolvedProvider: "cpu",
					GPUName:          "Vulkan device 0",
					FallbackReason:   "preferred engine unavailable",
					NumThreads:       4,
					Loaded:           true,
					LastRTF:          0.42,
				},
				Injection: InjectionStatus{
					Engine:    "wtype",
					Available: true,
					LastError: "previous injection failed",
				},
				Focus: FocusStatus{
					Sway:        true,
					FocusedID:   42,
					FocusedName: "editor",
					AppID:       "example.editor",
					Class:       "Editor",
					Workspace:   "2",
					Output:      "DP-1",
				},
				LastError:              &ErrorInfo{Code: "recognition_failed", Message: "recognition failed"},
				LastWarning:            &ErrorInfo{Code: "capture_overrun", Message: "audio was dropped"},
				LastTranscriptRedacted: true,
				LastUninjectedText:     "<redacted>",
				LastTranscript:         "<redacted>",
				UptimeSeconds:          123.5,
			},
		},
		{name: "empty", status: Status{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertJSONGolden(t, filepath.Join("testdata", "status_"+tt.name+".golden.json"), tt.status)
		})
	}
}

func assertJSONGolden(t *testing.T, path string, value any) {
	t.Helper()
	got, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if *update {
		if err := os.WriteFile(path, got, 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("JSON does not match %s; run go test -update to refresh\n\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}
