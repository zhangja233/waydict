package model

import "path/filepath"

const (
	ParakeetV3Int8ID     = "parakeet-tdt-0.6b-v3-int8"
	SherpaParakeetV3Int8 = "sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8"
	ParakeetV3ArchiveURL = "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8.tar.bz2"
	DefaultChecksumFile  = "checksums.sha256"
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

func Paths(dir string) []string {
	files := RequiredFiles()
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = filepath.Join(dir, f.Name)
	}
	return out
}
