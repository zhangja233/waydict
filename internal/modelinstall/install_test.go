package modelinstall

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"waydict/internal/model"
)

func TestUnpackTarWritesRegularFiles(t *testing.T) {
	dir := t.TempDir()
	data := tarData(t, tarEntry{name: "model/tokens.txt", body: "a\nb\n"})
	if err := unpackTar(tar.NewReader(bytes.NewReader(data)), dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "model", "tokens.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a\nb\n" {
		t.Fatalf("tokens = %q", got)
	}
}

func TestUnpackTarRejectsParentPath(t *testing.T) {
	dir := t.TempDir()
	data := tarData(t, tarEntry{name: "../outside", body: "x"})
	if err := unpackTar(tar.NewReader(bytes.NewReader(data)), dir); err == nil {
		t.Fatal("expected unsafe archive path error")
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "outside")); !os.IsNotExist(err) {
		t.Fatalf("outside file was created: %v", err)
	}
}

func TestUnpackTarRejectsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	data := tarData(t, tarEntry{name: filepath.Join(dir, "outside"), body: "x"})
	if err := unpackTar(tar.NewReader(bytes.NewReader(data)), dir); err == nil {
		t.Fatal("expected unsafe archive path error")
	}
}

func TestWriteChecksumsMatchesRequiredFiles(t *testing.T) {
	dir := writeTinyModel(t)
	if err := writeChecksums(dir); err != nil {
		t.Fatal(err)
	}
	if res := model.CheckDir(dir, model.CheckOptions{}); !res.OK {
		t.Fatalf("check failed: %+v", res.Errors)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokens.txt"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if res := model.CheckDir(dir, model.CheckOptions{}); res.OK {
		t.Fatalf("check unexpectedly passed after checksum mismatch: %+v", res)
	}
}

func TestWriteMetadataFiles(t *testing.T) {
	dir := writeTinyModel(t)
	if err := writeMetadataFiles(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range model.MetadataFiles() {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if len(body) == 0 {
			t.Fatalf("%s was empty", name)
		}
	}
	if res := model.CheckDir(dir, model.CheckOptions{}); !res.OK || len(res.Warnings) != 0 {
		t.Fatalf("check = ok:%t errors:%v warnings:%v", res.OK, res.Errors, res.Warnings)
	}
}

func TestActivateInstallUpdatesCurrentSymlink(t *testing.T) {
	base := t.TempDir()
	final := filepath.Join(base, model.ParakeetV3Int8ID)
	if err := os.Mkdir(final, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(final, "marker"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	oldCurrent := filepath.Join(base, "old-current")
	if err := os.Mkdir(oldCurrent, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(oldCurrent, filepath.Join(base, "current")); err != nil {
		t.Fatal(err)
	}
	staging := final + ".new"
	if err := os.Mkdir(staging, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "marker"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := activateInstall(base, staging)
	if err != nil {
		t.Fatal(err)
	}
	if got != final {
		t.Fatalf("final path = %q, want %q", got, final)
	}
	body, err := os.ReadFile(filepath.Join(final, "marker"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "new" {
		t.Fatalf("marker = %q, want new", body)
	}
	if _, err := os.Stat(final + ".old"); !os.IsNotExist(err) {
		t.Fatalf("backup remains: %v", err)
	}
	target, err := os.Readlink(filepath.Join(base, "current"))
	if err != nil {
		t.Fatal(err)
	}
	if target != final {
		t.Fatalf("current target = %q, want %q", target, final)
	}
}

func writeTinyModel(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"encoder.int8.onnx": "encoder",
		"decoder.int8.onnx": "decoder",
		"joiner.int8.onnx":  "joiner",
		"tokens.txt":        "a\nb\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type tarEntry struct {
	name string
	body string
}

func tarData(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, entry := range entries {
		hdr := &tar.Header{
			Name: entry.name,
			Mode: 0644,
			Size: int64(len(entry.body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
