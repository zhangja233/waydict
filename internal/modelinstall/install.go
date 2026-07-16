package modelinstall

import (
	"archive/tar"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"waydict/internal/config"
	"waydict/internal/model"
)

type InstallOptions struct {
	Dir           string
	URL           string
	StateDir      string
	CacheDir      string
	Progress      ProgressSink
	RetainPartial bool
	HTTPClient    *http.Client
	lock          *InstallLock
}

type Progress struct {
	Item            string
	Phase           string
	BytesDownloaded int64
	TotalBytes      int64
}

type ProgressSink func(Progress)

type progressReporter struct {
	mu        sync.Mutex
	sink      ProgressSink
	last      time.Time
	lastItem  string
	lastPhase string
}

func newProgressReporter(sink ProgressSink) *progressReporter {
	return &progressReporter{sink: sink}
}

func (r *progressReporter) send(progress Progress) {
	if r == nil || r.sink == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	phaseChanged := progress.Item != r.lastItem || progress.Phase != r.lastPhase
	if !phaseChanged && progress.Phase != "complete" && now.Sub(r.last) < 100*time.Millisecond {
		return
	}
	r.last = now
	r.lastItem = progress.Item
	r.lastPhase = progress.Phase
	r.sink(progress)
}

func InstallParakeetUnifiedFP32(ctx context.Context, opts InstallOptions) (string, error) {
	return withLockResult(ctx, opts, func(locked InstallOptions) (string, error) {
		return installParakeetUnifiedFP32(ctx, locked)
	})
}

func installParakeetUnifiedFP32(ctx context.Context, opts InstallOptions) (string, error) {
	reporter := newProgressReporter(opts.Progress)
	item := model.ParakeetUnifiedFP32ID
	reporter.send(Progress{Item: item, Phase: "resolving"})
	base, err := modelRoot(opts.Dir)
	if err != nil {
		return "", err
	}
	sourceBase := opts.URL
	if sourceBase == "" {
		sourceBase = model.ParakeetUnifiedFP32BaseURL
	}
	if err := preparePrivateDir(base); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(base, "."+model.ParakeetUnifiedFP32ID+"-*.partial")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	extracted := filepath.Join(tmp, model.SherpaParakeetUnifiedFP32)
	if err := os.MkdirAll(extracted, 0700); err != nil {
		return "", err
	}
	if err := downloadRequiredFiles(ctx, sourceBase, extracted, model.ParakeetUnifiedFP32Files(), opts, reporter); err != nil {
		return "", err
	}
	if err := writeMetadataFiles(extracted, parakeetUnifiedFP32Metadata); err != nil {
		return "", err
	}
	if err := writeChecksums(extracted, model.ParakeetUnifiedFP32Files()); err != nil {
		return "", err
	}
	reporter.send(Progress{Item: item, Phase: "verifying"})
	res := model.CheckDir(extracted, model.CheckOptions{StrictSizes: true})
	if !res.OK {
		return "", fmt.Errorf("downloaded model failed validation: %s", strings.Join(res.Errors, "; "))
	}
	if err := syncTree(extracted); err != nil {
		return "", err
	}
	reporter.send(Progress{Item: item, Phase: "installing"})
	path, err := activateInstall(base, model.ParakeetUnifiedFP32ID, extracted)
	if err == nil {
		reporter.send(Progress{Item: item, Phase: "complete"})
	}
	return path, err
}

func InstallParakeetV3Int8(ctx context.Context, opts InstallOptions) (string, error) {
	return withLockResult(ctx, opts, func(locked InstallOptions) (string, error) {
		return installParakeetV3Int8(ctx, locked)
	})
}

func installParakeetV3Int8(ctx context.Context, opts InstallOptions) (string, error) {
	reporter := newProgressReporter(opts.Progress)
	item := model.ParakeetV3Int8ID
	reporter.send(Progress{Item: item, Phase: "resolving"})
	base, err := modelRoot(opts.Dir)
	if err != nil {
		return "", err
	}
	url := opts.URL
	if url == "" {
		url = model.ParakeetV3ArchiveURL
	}
	if err := preparePrivateDir(base); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(base, "."+model.ParakeetV3Int8ID+"-*.partial")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	archivePath, cleanup, err := downloadPartial(ctx, url, filepath.Base(url), opts, reporter)
	if err != nil {
		return "", err
	}
	defer cleanup()
	reporter.send(Progress{Item: item, Phase: "verifying"})
	if err := unpackTarBz2(archivePath, tmp); err != nil {
		return "", err
	}
	extracted := filepath.Join(tmp, model.SherpaParakeetV3Int8)
	if _, err := os.Stat(extracted); err != nil {
		return "", err
	}
	if err := writeMetadataFiles(extracted, parakeetV3Int8Metadata); err != nil {
		return "", err
	}
	if err := writeChecksums(extracted, model.ParakeetV3Int8Files()); err != nil {
		return "", err
	}
	res := model.CheckDir(extracted, model.CheckOptions{StrictSizes: true})
	if !res.OK {
		return "", fmt.Errorf("downloaded model failed validation: %s", strings.Join(res.Errors, "; "))
	}
	if err := syncTree(extracted); err != nil {
		return "", err
	}
	reporter.send(Progress{Item: item, Phase: "installing"})
	path, err := activateInstall(base, model.ParakeetV3Int8ID, extracted)
	if err == nil {
		reporter.send(Progress{Item: item, Phase: "complete"})
	}
	return path, err
}

func InstallSileroVAD(ctx context.Context, opts InstallOptions) (string, error) {
	return withLockResult(ctx, opts, func(locked InstallOptions) (string, error) {
		return installSileroVAD(ctx, locked)
	})
}

func installSileroVAD(ctx context.Context, opts InstallOptions) (string, error) {
	reporter := newProgressReporter(opts.Progress)
	item := model.SileroVADFile
	reporter.send(Progress{Item: item, Phase: "resolving"})
	base, err := modelRoot(opts.Dir)
	if err != nil {
		return "", err
	}
	url := opts.URL
	if url == "" {
		url = model.SileroVADURL
	}
	if err := preparePrivateDir(base); err != nil {
		return "", err
	}
	staged, cleanup, err := downloadPartial(ctx, url, item, opts, reporter)
	if err != nil {
		return "", err
	}
	defer cleanup()
	reporter.send(Progress{Item: item, Phase: "verifying"})
	st, err := os.Stat(staged)
	if err != nil {
		return "", err
	}
	if st.Size() < model.MinSileroVADSize {
		return "", fmt.Errorf("downloaded silero model is implausibly small (%d bytes); check the URL", st.Size())
	}
	final := filepath.Join(base, model.SileroVADFile)
	reporter.send(Progress{Item: item, Phase: "installing", BytesDownloaded: st.Size(), TotalBytes: st.Size()})
	if err := activateFile(staged, final); err != nil {
		return "", err
	}
	reporter.send(Progress{Item: item, Phase: "complete", BytesDownloaded: st.Size(), TotalBytes: st.Size()})
	return final, nil
}

func InstallWhisper(ctx context.Context, name string, opts InstallOptions) (string, error) {
	return withLockResult(ctx, opts, func(locked InstallOptions) (string, error) {
		asset, err := model.WhisperAssetForName(name)
		if err != nil {
			return "", err
		}
		return installWhisperAssetLocked(ctx, asset, locked)
	})
}

func installWhisperAsset(ctx context.Context, asset model.WhisperAsset, opts InstallOptions) (string, error) {
	return withLockResult(ctx, opts, func(locked InstallOptions) (string, error) {
		return installWhisperAssetLocked(ctx, asset, locked)
	})
}

func installWhisperAssetLocked(ctx context.Context, asset model.WhisperAsset, opts InstallOptions) (string, error) {
	reporter := newProgressReporter(opts.Progress)
	reporter.send(Progress{Item: asset.Model, Phase: "resolving", TotalBytes: asset.Size})
	base, err := modelRoot(opts.Dir)
	if err != nil {
		return "", err
	}
	whisperDir := filepath.Join(base, "whisper")
	if err := preparePrivateDir(whisperDir); err != nil {
		return "", err
	}
	url := opts.URL
	if url == "" {
		url = asset.URL
	}
	staged, cleanup, err := downloadPartial(ctx, url, asset.Model, opts, reporter)
	if err != nil {
		return "", err
	}
	defer cleanup()
	reporter.send(Progress{Item: asset.Model, Phase: "verifying", TotalBytes: asset.Size})
	st, err := os.Stat(staged)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("downloaded whisper model is not a regular file")
	}
	if asset.Size > 0 && st.Size() != asset.Size {
		return "", fmt.Errorf("downloaded whisper model has size %d, want %d", st.Size(), asset.Size)
	}
	if asset.Size == 0 && st.Size() < model.MinUnknownWhisperModelSize {
		return "", fmt.Errorf("downloaded whisper model is implausibly small (%d bytes); check the URL", st.Size())
	}
	if asset.SHA256 == "" {
		fmt.Fprintf(os.Stderr, "warning: integrity is not pinned for non-catalog whisper model %q; download was size-checked only\n", asset.Model)
	} else {
		got, err := fileSHA256(staged)
		if err != nil {
			return "", err
		}
		if !strings.EqualFold(got, asset.SHA256) {
			return "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset.File, got, asset.SHA256)
		}
	}
	final := filepath.Join(whisperDir, asset.File)
	reporter.send(Progress{Item: asset.Model, Phase: "installing", BytesDownloaded: st.Size(), TotalBytes: asset.Size})
	if err := activateFile(staged, final); err != nil {
		return "", err
	}
	reporter.send(Progress{Item: asset.Model, Phase: "complete", BytesDownloaded: st.Size(), TotalBytes: asset.Size})
	return final, nil
}

func modelRoot(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	return config.DefaultModelsRoot(), nil
}

func activateInstall(base, modelID, staging string) (string, error) {
	final := filepath.Join(base, modelID)
	if _, err := os.Stat(final); err == nil {
		if err := exchangePaths(staging, final); err != nil {
			return "", err
		}
		if err := os.RemoveAll(staging); err != nil {
			return "", err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(staging, final); err != nil {
			return "", err
		}
	} else {
		return "", err
	}
	if err := syncDir(base); err != nil {
		return "", err
	}
	link := filepath.Join(base, "current")
	tmpLink := filepath.Join(base, ".current.new")
	_ = os.Remove(tmpLink)
	if err := os.Symlink(final, tmpLink); err != nil {
		return "", err
	}
	if err := os.Rename(tmpLink, link); err != nil {
		_ = os.Remove(tmpLink)
		return "", err
	}
	if err := syncDir(base); err != nil {
		return "", err
	}
	return final, nil
}

func activateFile(source, final string) error {
	if err := preparePrivateDir(filepath.Dir(final)); err != nil {
		return err
	}
	staging, err := os.CreateTemp(filepath.Dir(final), "."+filepath.Base(final)+"-*.partial")
	if err != nil {
		return err
	}
	stagingPath := staging.Name()
	defer os.Remove(stagingPath)
	if err := staging.Chmod(0600); err != nil {
		_ = staging.Close()
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		_ = staging.Close()
		return err
	}
	_, copyErr := io.Copy(staging, input)
	closeInputErr := input.Close()
	if copyErr == nil {
		copyErr = staging.Sync()
	}
	closeStagingErr := staging.Close()
	if err := errors.Join(copyErr, closeInputErr, closeStagingErr); err != nil {
		return err
	}
	if err := os.Rename(stagingPath, final); err != nil {
		return err
	}
	return syncDir(filepath.Dir(final))
}

func copyFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Chmod(mode); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func syncTree(root string) error {
	var directories []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			if err := os.Chmod(path, 0600); err != nil {
				return err
			}
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			syncErr := file.Sync()
			closeErr := file.Close()
			return errors.Join(syncErr, closeErr)
		}
		if info.IsDir() {
			if err := os.Chmod(path, 0700); err != nil {
				return err
			}
			directories = append(directories, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := syncDir(directories[index]); err != nil {
			return err
		}
	}
	return nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func downloadRequiredFiles(ctx context.Context, baseURL, dir string, files []model.RequiredFile, opts InstallOptions, reporter *progressReporter) error {
	for _, req := range files {
		dst := filepath.Join(dir, req.Name)
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return err
		}
		partial, cleanup, err := downloadPartial(ctx, fileURL(baseURL, req.Name), req.Name, opts, reporter)
		if err != nil {
			return fmt.Errorf("%s: %w", req.Name, err)
		}
		st, err := os.Stat(partial)
		if err != nil {
			cleanup()
			return err
		}
		if st.Size() < req.MinSize {
			cleanup()
			return fmt.Errorf("%s is implausibly small (%d bytes); check the URL", req.Name, st.Size())
		}
		reporter.send(Progress{Item: req.Name, Phase: "verifying", BytesDownloaded: st.Size(), TotalBytes: st.Size()})
		if err := copyFile(partial, dst, 0600); err != nil {
			cleanup()
			return err
		}
		cleanup()
	}
	return nil
}

func fileURL(base, name string) string {
	return strings.TrimRight(base, "/") + "/" + name
}

func downloadPartial(ctx context.Context, url, item string, opts InstallOptions, reporter *progressReporter) (string, func(), error) {
	dir := cacheDownloadsDir(opts)
	if err := preparePrivateDir(dir); err != nil {
		return "", func() {}, err
	}
	prefix := "." + safeCacheName(item) + "-"
	file, err := os.CreateTemp(dir, prefix+"*.partial")
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	cleanup := func() {
		_ = file.Close()
		if !opts.RetainPartial {
			_ = os.Remove(path)
		}
	}
	if err := os.Chmod(path, 0600); err != nil {
		cleanup()
		return "", func() {}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		cleanup()
		return "", func() {}, fmt.Errorf("download failed: %s", resp.Status)
	}
	total := resp.ContentLength
	if total < 0 {
		total = 0
	}
	reporter.send(Progress{Item: item, Phase: "downloading", TotalBytes: total})
	buffer := make([]byte, 128*1024)
	var downloaded int64
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			written, writeErr := file.Write(buffer[:n])
			downloaded += int64(written)
			reporter.send(Progress{Item: item, Phase: "downloading", BytesDownloaded: downloaded, TotalBytes: total})
			if writeErr != nil {
				cleanup()
				return "", func() {}, writeErr
			}
			if written != n {
				cleanup()
				return "", func() {}, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			cleanup()
			return "", func() {}, readErr
		}
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	reporter.send(Progress{Item: item, Phase: "downloading", BytesDownloaded: downloaded, TotalBytes: total})
	return path, cleanup, nil
}

func safeCacheName(item string) string {
	if item == "" {
		return "model"
	}
	var builder strings.Builder
	for _, value := range item {
		if value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '-' || value == '_' || value == '.' {
			builder.WriteRune(value)
		} else {
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "model"
	}
	return builder.String()
}

func unpackTarBz2(path, dst string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return unpackTar(tar.NewReader(bzip2.NewReader(f)), dst)
}

func unpackTar(tr *tar.Reader, dst string) error {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dst, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, modeOrDefault(hdr.FileInfo().Mode(), 0700)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, modeOrDefault(hdr.FileInfo().Mode(), 0600))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}

func safeJoin(base, name string) (string, error) {
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean == ".." {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return filepath.Join(base, clean), nil
}

func modeOrDefault(mode os.FileMode, def os.FileMode) os.FileMode {
	if mode == 0 {
		return def
	}
	return mode
}

func writeChecksums(dir string, files []model.RequiredFile) error {
	out, err := os.OpenFile(filepath.Join(dir, model.DefaultChecksumFile), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	for _, req := range files {
		path := filepath.Join(dir, req.Name)
		sum, err := fileSHA256(path)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "%s  %s\n", sum, req.Name); err != nil {
			return err
		}
	}
	return nil
}

type metadataFiles struct {
	License   string
	ModelCard string
}

func writeMetadataFiles(dir string, metadata metadataFiles) error {
	files := map[string]string{
		"LICENSE":       metadata.License,
		"MODEL_CARD.md": metadata.ModelCard,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			return err
		}
	}
	return nil
}

var parakeetUnifiedFP32Metadata = metadataFiles{
	License: `Parakeet Unified English 0.6B model notice

The installed ONNX model files are converted from NVIDIA parakeet-unified-en-0.6b and are distributed under the NVIDIA Open Model License.

Review the upstream model card and the sherpa-onnx conversion package notices before redistributing model assets:
https://huggingface.co/nvidia/parakeet-unified-en-0.6b
https://huggingface.co/csukuangfj2/sherpa-onnx-nemo-parakeet-unified-en-0.6b-non-streaming
https://github.com/k2-fsa/sherpa-onnx
`,
	ModelCard: `# Parakeet Unified English 0.6B FP32

These files are the sherpa-onnx FP32 non-streaming conversion of NVIDIA parakeet-unified-en-0.6b for local CPU speech recognition.

Runtime assumptions used by waydict:

- 16 kHz mono audio input.
- sherpa-onnx transducer model type: nemo_transducer.
- CPU provider.
- Non-streaming/offline recognition.

Upstream references:

- NVIDIA model card: https://huggingface.co/nvidia/parakeet-unified-en-0.6b
- sherpa-onnx conversion package: https://huggingface.co/csukuangfj2/sherpa-onnx-nemo-parakeet-unified-en-0.6b-non-streaming
- sherpa-onnx usage notes: https://k2-fsa.github.io/sherpa/onnx/pretrained_models/offline-transducer/nemo-transducer-models.html
`,
}

var parakeetV3Int8Metadata = metadataFiles{
	License: `Parakeet-TDT-0.6B-v3 model notice

The installed ONNX model files are converted from NVIDIA parakeet-tdt-0.6b-v3 and are described by the upstream model card as CC-BY-4.0 licensed.

Review the upstream model card and the sherpa-onnx conversion package notices before redistributing model assets:
https://huggingface.co/nvidia/parakeet-tdt-0.6b-v3
https://github.com/k2-fsa/sherpa-onnx
`,

	ModelCard: `# Parakeet-TDT-0.6B-v3 INT8

These files are the sherpa-onnx INT8 conversion of NVIDIA parakeet-tdt-0.6b-v3 for local CPU speech recognition.

Runtime assumptions used by waydict:

- 16 kHz mono audio input.
- sherpa-onnx transducer model type: nemo_transducer.
- CPU provider.

Upstream references:

- NVIDIA model card: https://huggingface.co/nvidia/parakeet-tdt-0.6b-v3
- sherpa-onnx conversion package: https://github.com/k2-fsa/sherpa-onnx
`,
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
