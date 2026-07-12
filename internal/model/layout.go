package model

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	ParakeetUnifiedFP32ID      = "parakeet-unified-en-0.6b-fp32"
	SherpaParakeetUnifiedFP32  = "sherpa-onnx-nemo-parakeet-unified-en-0.6b-non-streaming"
	ParakeetUnifiedFP32BaseURL = "https://huggingface.co/csukuangfj2/sherpa-onnx-nemo-parakeet-unified-en-0.6b-non-streaming/resolve/main"

	ParakeetV3Int8ID     = "parakeet-tdt-0.6b-v3-int8"
	SherpaParakeetV3Int8 = "sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8"
	ParakeetV3ArchiveURL = "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8.tar.bz2"
	DefaultChecksumFile  = "checksums.sha256"

	SileroVADFile = "silero_vad.onnx"
	SileroVADURL  = "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/silero_vad.onnx"
	// MinSileroVADSize guards against truncated or HTML-error downloads; the real
	// asset is ~600 KiB and there is no published checksum to verify against.
	MinSileroVADSize = 64 * 1024

	WhisperSmallEnModel        = "ggml-small.en"
	WhisperMediumEnModel       = "ggml-medium.en"
	WhisperLargeV3TurboModel   = "ggml-large-v3-turbo"
	MinUnknownWhisperModelSize = 64 * 1024 * 1024
	whisperModelBaseURL        = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/"
)

type WhisperAsset struct {
	Model  string
	File   string
	URL    string
	Size   int64
	SHA256 string
}

func WhisperAssets() []WhisperAsset {
	return []WhisperAsset{
		{
			Model:  WhisperSmallEnModel,
			File:   "ggml-small.en.bin",
			URL:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.en.bin",
			Size:   487614201,
			SHA256: "c6138d6d58ecc8322097e0f987c32f1be8bb0a18532a3f88f734d1bbf9c41e5d",
		},
		{
			Model:  WhisperMediumEnModel,
			File:   "ggml-medium.en.bin",
			URL:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-medium.en.bin",
			Size:   1533774781,
			SHA256: "cc37e93478338ec7700281a7ac30a10128929eb8f427dda2e865faa8f6da4356",
		},
		{
			Model:  WhisperLargeV3TurboModel,
			File:   "ggml-large-v3-turbo.bin",
			URL:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo.bin",
			Size:   1624555275,
			SHA256: "1fc70f774d38eb169993ac391eea357ef47c88757ef72ee5943879b7e8e2bc69",
		},
	}
}

func WhisperAssetByModel(name string) (WhisperAsset, bool) {
	for _, asset := range WhisperAssets() {
		if asset.Model == name {
			return asset, true
		}
	}
	return WhisperAsset{}, false
}

func WhisperAssetForName(name string) (WhisperAsset, error) {
	if strings.TrimSpace(name) == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return WhisperAsset{}, fmt.Errorf("whisper model must be a non-empty bare name")
	}
	if asset, ok := WhisperAssetByModel(name); ok {
		return asset, nil
	}
	return WhisperAsset{
		Model: name,
		File:  name + ".bin",
		URL:   whisperModelBaseURL + name + ".bin",
	}, nil
}

func WhisperModelMinSize(name string) int64 {
	if asset, ok := WhisperAssetByModel(name); ok {
		return asset.Size
	}
	return MinUnknownWhisperModelSize
}

type RequiredFile struct {
	Name    string
	MinSize int64
}

func RequiredFiles() []RequiredFile {
	return ParakeetUnifiedFP32Files()
}

func ParakeetUnifiedFP32Files() []RequiredFile {
	return []RequiredFile{
		{Name: "encoder.onnx", MinSize: 32 * 1024 * 1024},
		{Name: "encoder.weights", MinSize: 2 * 1024 * 1024 * 1024},
		{Name: "decoder.onnx", MinSize: 20 * 1024 * 1024},
		{Name: "joiner.onnx", MinSize: 4 * 1024 * 1024},
		{Name: "tokens.txt", MinSize: 32},
	}
}

func ParakeetV3Int8Files() []RequiredFile {
	return []RequiredFile{
		{Name: "encoder.int8.onnx", MinSize: 100 * 1024 * 1024},
		{Name: "decoder.int8.onnx", MinSize: 1024 * 1024},
		{Name: "joiner.int8.onnx", MinSize: 1024 * 1024},
		{Name: "tokens.txt", MinSize: 32},
	}
}

func MetadataFiles() []string {
	return []string{"LICENSE", "MODEL_CARD.md"}
}

func Paths(dir string) []string {
	files := RequiredFiles()
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = filepath.Join(dir, f.Name)
	}
	return out
}
