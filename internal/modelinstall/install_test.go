package modelinstall

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"waydict/internal/apperr"
	"waydict/internal/config"
	"waydict/internal/model"
)

func TestInstallWhisperWritesVerifiedModel(t *testing.T) {
	body := bytes.Repeat([]byte("whisper"), 128)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	base := t.TempDir()
	asset := testWhisperAsset(body)
	path, err := installWhisperAsset(context.Background(), asset, InstallOptions{Dir: base, URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "whisper", asset.File)
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatal("installed whisper model differs from download")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != asset.File {
		t.Fatalf("whisper dir not clean after install: %v", entries)
	}
}

func TestInstallLockRejectsConcurrentInstaller(t *testing.T) {
	opts := InstallOptions{StateDir: t.TempDir(), CacheDir: t.TempDir()}
	lock, err := Acquire(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := Acquire(context.Background(), opts); apperr.Code(err) != apperr.CodeModelInstallBusy {
		t.Fatalf("second lock error = %v, want %s", err, apperr.CodeModelInstallBusy)
	}
	info, err := os.Stat(filepath.Join(opts.StateDir, installLockName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("lock mode = %o", info.Mode().Perm())
	}
}

func TestInstallLockRejectsSymlink(t *testing.T) {
	stateDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(stateDir, installLockName)); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(context.Background(), InstallOptions{StateDir: stateDir, CacheDir: t.TempDir()}); err == nil || apperr.Code(err) == apperr.CodeModelInstallBusy {
		t.Fatalf("symlink lock error = %v", err)
	}
}

func TestAcquireCleansOnlyOldCurrentUserPartials(t *testing.T) {
	models := t.TempDir()
	cache := t.TempDir()
	downloads := filepath.Join(cache, "downloads")
	if err := os.MkdirAll(downloads, 0700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(downloads, "old.partial")
	recent := filepath.Join(downloads, "recent.partial")
	keep := filepath.Join(downloads, "model.bin")
	for _, path := range []string{old, recent, keep} {
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	lock, err := Acquire(context.Background(), InstallOptions{Dir: models, StateDir: t.TempDir(), CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old partial remains: %v", err)
	}
	for _, path := range []string{recent, keep} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("preserved file %s: %v", path, err)
		}
	}
}

func TestInstallProgressReportsPhasesAndCancellationCleansPartial(t *testing.T) {
	body := bytes.Repeat([]byte("progress"), 64*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		for offset := 0; offset < len(body); offset += 4096 {
			end := min(offset+4096, len(body))
			if _, err := w.Write(body[offset:end]); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			time.Sleep(time.Millisecond)
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	var phases []string
	opts := InstallOptions{
		Dir:      t.TempDir(),
		StateDir: t.TempDir(),
		CacheDir: t.TempDir(),
		URL:      srv.URL,
		Progress: func(progress Progress) {
			phases = append(phases, progress.Phase)
			if progress.Phase == "downloading" && progress.BytesDownloaded > 0 {
				cancel()
			}
		},
	}
	asset := testWhisperAsset(body)
	if _, err := installWhisperAsset(ctx, asset, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("install error = %v, want cancellation", err)
	}
	if !slices.Contains(phases, "resolving") || !slices.Contains(phases, "downloading") {
		t.Fatalf("progress phases = %v", phases)
	}
	entries, err := os.ReadDir(filepath.Join(opts.CacheDir, "downloads"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial downloads remain after cancellation: %v", entries)
	}
}

func TestInstallWhisperCatalogAssetRejectsChecksumMismatchAndPreservesExisting(t *testing.T) {
	body := []byte("replacement")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	base := t.TempDir()
	asset := testWhisperAsset(body)
	asset.SHA256 = fmt.Sprintf("%064d", 0)
	final := filepath.Join(base, "whisper", asset.File)
	if err := os.MkdirAll(filepath.Dir(final), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final, []byte("current"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := installWhisperAsset(context.Background(), asset, InstallOptions{Dir: base, URL: srv.URL}); err == nil {
		t.Fatal("expected checksum mismatch")
	}
	got, err := os.ReadFile(final)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "current" {
		t.Fatalf("existing model changed after rejected download: %q", got)
	}
}

func TestInstallWhisperUnknownNameUsesMinimumSize(t *testing.T) {
	size := int64(model.MinUnknownWhisperModelSize + 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.CopyN(w, zeroReader{}, size)
	}))
	defer srv.Close()

	oldStderr := os.Stderr
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = stderr
	t.Cleanup(func() { os.Stderr = oldStderr })

	base := t.TempDir()
	path, err := InstallWhisper(context.Background(), "ggml-base.en", InstallOptions{Dir: base, URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := stderr.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stderr = oldStderr
	warning, err := os.ReadFile(stderr.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(warning, []byte("integrity is not pinned")); got != 1 {
		t.Fatalf("warning count = %d, stderr=%q", got, warning)
	}
	want := filepath.Join(base, "whisper", "ggml-base.en.bin")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if st, err := os.Stat(path); err != nil || st.Size() != size {
		t.Fatalf("installed file stat = %v, err = %v", st, err)
	}
}

func TestInstallWhisperUnknownNameRejectsTinyDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tiny"))
	}))
	defer srv.Close()
	base := t.TempDir()
	if _, err := InstallWhisper(context.Background(), "ggml-base.en", InstallOptions{Dir: base, URL: srv.URL}); err == nil {
		t.Fatal("expected error for implausibly small download")
	}
	if _, err := os.Stat(filepath.Join(base, "whisper", "ggml-base.en.bin")); !os.IsNotExist(err) {
		t.Fatalf("model file should not exist after rejected download: %v", err)
	}
}

func TestModelRootUsesConfiguredDefault(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	got, err := modelRoot("")
	if err != nil {
		t.Fatal(err)
	}
	want := config.DefaultModelsRoot()
	if got != want {
		t.Fatalf("modelRoot() = %q, want %q", got, want)
	}
}

func TestInstallWhisperOverwritesExistingModel(t *testing.T) {
	body := []byte("replacement")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	base := t.TempDir()
	asset := testWhisperAsset(body)
	final := filepath.Join(base, "whisper", asset.File)
	if err := os.MkdirAll(filepath.Dir(final), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final, []byte("current"), 0644); err != nil {
		t.Fatal(err)
	}
	path, err := installWhisperAsset(context.Background(), asset, InstallOptions{Dir: base, URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("installed body = %q, want %q", got, body)
	}
}

func TestInstallSileroVADWritesModel(t *testing.T) {
	body := bytes.Repeat([]byte("x"), model.MinSileroVADSize+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	base := t.TempDir()
	path, err := InstallSileroVAD(context.Background(), InstallOptions{Dir: base, URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, model.SileroVADFile)
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(body) {
		t.Fatalf("size = %d, want %d", len(got), len(body))
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != model.SileroVADFile {
		t.Fatalf("base dir not clean after install: %v", entries)
	}
}

func TestInstallSileroVADRejectsTinyDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()
	base := t.TempDir()
	if _, err := InstallSileroVAD(context.Background(), InstallOptions{Dir: base, URL: srv.URL}); err == nil {
		t.Fatal("expected error for implausibly small download")
	}
	if _, err := os.Stat(filepath.Join(base, model.SileroVADFile)); !os.IsNotExist(err) {
		t.Fatalf("model file should not exist after rejected download: %v", err)
	}
}

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
	if err := writeChecksums(dir, model.RequiredFiles()); err != nil {
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
	if err := writeMetadataFiles(dir, parakeetUnifiedFP32Metadata); err != nil {
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
	final := filepath.Join(base, model.ParakeetUnifiedFP32ID)
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
	got, err := activateInstall(base, model.ParakeetUnifiedFP32ID, staging)
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
		"encoder.onnx":    "encoder",
		"encoder.weights": "weights",
		"decoder.onnx":    "decoder",
		"joiner.onnx":     "joiner",
		"tokens.txt":      "a\nb\n",
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

func testWhisperAsset(body []byte) model.WhisperAsset {
	sum := sha256.Sum256(body)
	return model.WhisperAsset{
		Model:  model.WhisperSmallEnModel,
		File:   "ggml-small.en.bin",
		Size:   int64(len(body)),
		SHA256: fmt.Sprintf("%x", sum),
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
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
