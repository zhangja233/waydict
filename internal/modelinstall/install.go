package modelinstall

import (
	"archive/tar"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"waydict/internal/config"
	"waydict/internal/model"
)

type InstallOptions struct {
	Dir string
	URL string
}

func InstallParakeetUnifiedFP32(ctx context.Context, opts InstallOptions) (string, error) {
	base, err := modelRoot(opts.Dir)
	if err != nil {
		return "", err
	}
	sourceBase := opts.URL
	if sourceBase == "" {
		sourceBase = model.ParakeetUnifiedFP32BaseURL
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(base, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	extracted := filepath.Join(tmp, model.SherpaParakeetUnifiedFP32)
	if err := os.MkdirAll(extracted, 0755); err != nil {
		return "", err
	}
	if err := downloadRequiredFiles(ctx, sourceBase, extracted, model.ParakeetUnifiedFP32Files()); err != nil {
		return "", err
	}
	final := filepath.Join(base, model.ParakeetUnifiedFP32ID)
	staging := final + ".new"
	_ = os.RemoveAll(staging)
	if err := os.Rename(extracted, staging); err != nil {
		return "", err
	}
	if err := writeMetadataFiles(staging, parakeetUnifiedFP32Metadata); err != nil {
		return "", err
	}
	if err := writeChecksums(staging, model.ParakeetUnifiedFP32Files()); err != nil {
		return "", err
	}
	res := model.CheckDir(staging, model.CheckOptions{StrictSizes: true})
	if !res.OK {
		return "", fmt.Errorf("downloaded model failed validation: %s", strings.Join(res.Errors, "; "))
	}
	return activateInstall(base, model.ParakeetUnifiedFP32ID, staging)
}

func InstallParakeetV3Int8(ctx context.Context, opts InstallOptions) (string, error) {
	base, err := modelRoot(opts.Dir)
	if err != nil {
		return "", err
	}
	url := opts.URL
	if url == "" {
		url = model.ParakeetV3ArchiveURL
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(base, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	archivePath := filepath.Join(tmp, filepath.Base(url))
	if err := download(ctx, url, archivePath); err != nil {
		return "", err
	}
	if err := unpackTarBz2(archivePath, tmp); err != nil {
		return "", err
	}
	extracted := filepath.Join(tmp, model.SherpaParakeetV3Int8)
	if _, err := os.Stat(extracted); err != nil {
		return "", err
	}
	final := filepath.Join(base, model.ParakeetV3Int8ID)
	staging := final + ".new"
	_ = os.RemoveAll(staging)
	if err := os.Rename(extracted, staging); err != nil {
		return "", err
	}
	if err := writeMetadataFiles(staging, parakeetV3Int8Metadata); err != nil {
		return "", err
	}
	if err := writeChecksums(staging, model.ParakeetV3Int8Files()); err != nil {
		return "", err
	}
	return activateInstall(base, model.ParakeetV3Int8ID, staging)
}

func InstallSileroVAD(ctx context.Context, opts InstallOptions) (string, error) {
	base, err := modelRoot(opts.Dir)
	if err != nil {
		return "", err
	}
	url := opts.URL
	if url == "" {
		url = model.SileroVADURL
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(base, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	staged := filepath.Join(tmp, model.SileroVADFile)
	if err := download(ctx, url, staged); err != nil {
		return "", err
	}
	st, err := os.Stat(staged)
	if err != nil {
		return "", err
	}
	if st.Size() < model.MinSileroVADSize {
		return "", fmt.Errorf("downloaded silero model is implausibly small (%d bytes); check the URL", st.Size())
	}
	final := filepath.Join(base, model.SileroVADFile)
	if err := os.Rename(staged, final); err != nil {
		return "", err
	}
	return final, nil
}

func InstallWhisper(ctx context.Context, name string, opts InstallOptions) (string, error) {
	asset, err := model.WhisperAssetForName(name)
	if err != nil {
		return "", err
	}
	return installWhisperAsset(ctx, asset, opts)
}

func installWhisperAsset(ctx context.Context, asset model.WhisperAsset, opts InstallOptions) (string, error) {
	base, err := modelRoot(opts.Dir)
	if err != nil {
		return "", err
	}
	whisperDir := filepath.Join(base, "whisper")
	if err := os.MkdirAll(whisperDir, 0755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(whisperDir, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	staged := filepath.Join(tmp, asset.File)
	url := opts.URL
	if url == "" {
		url = asset.URL
	}
	if err := download(ctx, url, staged); err != nil {
		return "", err
	}
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
	if err := os.Rename(staged, final); err != nil {
		return "", err
	}
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
	backup := final + ".old"
	_ = os.RemoveAll(backup)
	if _, err := os.Stat(final); err == nil {
		if err := os.Rename(final, backup); err != nil {
			return "", err
		}
	}
	if err := os.Rename(staging, final); err != nil {
		_ = os.Rename(backup, final)
		return "", err
	}
	_ = os.RemoveAll(backup)
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
	return final, nil
}

func downloadRequiredFiles(ctx context.Context, baseURL, dir string, files []model.RequiredFile) error {
	for _, req := range files {
		dst := filepath.Join(dir, req.Name)
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		if err := download(ctx, fileURL(baseURL, req.Name), dst); err != nil {
			return fmt.Errorf("%s: %w", req.Name, err)
		}
		st, err := os.Stat(dst)
		if err != nil {
			return err
		}
		if st.Size() < req.MinSize {
			return fmt.Errorf("%s is implausibly small (%d bytes); check the URL", req.Name, st.Size())
		}
	}
	return nil
}

func fileURL(base, name string) string {
	return strings.TrimRight(base, "/") + "/" + name
}

func download(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
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
			if err := os.MkdirAll(target, modeOrDefault(hdr.FileInfo().Mode(), 0755)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, modeOrDefault(hdr.FileInfo().Mode(), 0644))
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
	out, err := os.Create(filepath.Join(dir, model.DefaultChecksumFile))
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
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
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
