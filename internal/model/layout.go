package model

import "path/filepath"

const (
	ParakeetV3Int8ID     = "parakeet-tdt-0.6b-v3-int8"
	SherpaParakeetV3Int8 = "sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8"
	ParakeetV3ArchiveURL = "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8.tar.bz2"
	DefaultChecksumFile  = "checksums.sha256"

	SileroVADFile = "silero_vad.onnx"
	SileroVADURL  = "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/silero_vad.onnx"
	// MinSileroVADSize guards against truncated or HTML-error downloads; the real
	// asset is ~600 KiB and there is no published checksum to verify against.
	MinSileroVADSize = 64 * 1024
)

type RequiredFile struct {
	Name    string
	MinSize int64
}

func RequiredFiles() []RequiredFile {
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
