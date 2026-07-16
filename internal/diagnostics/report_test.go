package diagnostics

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"waydict/pkg/api"
)

func TestReportRedactsTranscriptTokensAndHome(t *testing.T) {
	const transcript = "PR7-TRANSCRIPT-MUST-NOT-APPEAR"
	output := Build(Snapshot{
		BundlePath: "/Users/tester/Applications/Waydict.app",
		ConfigPath: "/Users/tester/Library/Application Support/Waydict/config.toml",
		LastError:  &api.ErrorInfo{Code: "test", Message: "token=https://example.test/?access_token=hunter2"},
		RecentLogLines: []string{
			`time=now level=INFO msg="transcript accepted" transcript="` + transcript + `"`,
			`time=now level=INFO msg=request url=https://example.test/model?token=hunter2`,
		},
	}, "/Users/tester")
	encoded, err := json.Marshal(output.Data)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{output.Text, string(encoded)} {
		if strings.Contains(value, transcript) || strings.Contains(value, "hunter2") || strings.Contains(value, "/Users/tester") {
			t.Fatalf("sensitive value reached diagnostics: %s", value)
		}
	}
}

func TestReportSchemaHasNoTranscriptFields(t *testing.T) {
	output := Build(Snapshot{}, "")
	encoded, err := json.Marshal(output.Data)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"last_transcript", "last_uninjected_text"} {
		if strings.Contains(string(encoded), key) {
			t.Fatalf("diagnostics contains forbidden field %q: %s", key, encoded)
		}
	}
}

func TestRedactionProbePrintsSanitizedDiagnostics(t *testing.T) {
	const transcript = "WAYDICT-REDACTION-PROBE-7F31"
	output := Build(Snapshot{RecentLogLines: []string{"transcript=" + transcript}}, "")
	if strings.Contains(output.Text, transcript) {
		t.Fatal("redaction probe reached diagnostics")
	}
	fmt.Println(output.Text)
}
