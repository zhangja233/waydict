package sherpa

import (
	"path/filepath"

	"waydict/internal/config"
)

type Paths struct {
	Encoder string
	Decoder string
	Joiner  string
	Tokens  string
}

func ModelPaths(cfg config.ASR) Paths {
	return Paths{
		Encoder: filepath.Join(cfg.ModelDir, cfg.Encoder),
		Decoder: filepath.Join(cfg.ModelDir, cfg.Decoder),
		Joiner:  filepath.Join(cfg.ModelDir, cfg.Joiner),
		Tokens:  filepath.Join(cfg.ModelDir, cfg.Tokens),
	}
}
