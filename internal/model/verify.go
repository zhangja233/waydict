package model

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"waydict/internal/asr"
	"waydict/internal/config"
)

type CheckOptions struct {
	StrictSizes bool
}

type CheckResult struct {
	Dir       string         `json:"dir"`
	Engine    string         `json:"engine,omitempty"`
	Validated []CheckedModel `json:"validated,omitempty"`
	OK        bool           `json:"ok"`
	Items     []CheckItem    `json:"items"`
	Errors    []string       `json:"errors,omitempty"`
	Warnings  []string       `json:"warnings,omitempty"`
}

type CheckedModel struct {
	Engine string `json:"engine"`
	Name   string `json:"name"`
	Path   string `json:"path"`
}

type CheckItem struct {
	Path    string `json:"path"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	Size    int64  `json:"size,omitempty"`
}

type requiredPath struct {
	Name    string
	Path    string
	MinSize int64
}

func CheckConfig(cfg config.Config, opts CheckOptions) CheckResult {
	switch cfg.ASR.Engine {
	case asr.EngineSherpa:
		return checkSherpaConfig(cfg, opts)
	case asr.EngineWhisper:
		return checkWhisperConfig(cfg, opts)
	case asr.EngineAuto:
		return checkAutoConfig(cfg, opts)
	default:
		res := CheckResult{Engine: cfg.ASR.Engine, OK: true}
		res.addErr(fmt.Sprintf("unknown asr.engine %q", cfg.ASR.Engine))
		return res
	}
}

func checkSherpaConfig(cfg config.Config, opts CheckOptions) CheckResult {
	res := CheckResult{Dir: cfg.ASR.ModelDir, Engine: asr.EngineSherpa, OK: true}
	checkRequiredPaths(&res, configuredRequiredPaths(cfg), opts)
	if err := checkTokens(cfg.TokensPath()); err != nil {
		res.addErr(err.Error())
	}
	checkMetadataAndChecksums(&res, cfg.ASR.ModelDir)
	if cfg.ASR.Provider != asr.ProviderCPU {
		res.addErr("asr.provider must be cpu")
	}
	if res.OK {
		res.Validated = append(res.Validated, CheckedModel{Engine: asr.EngineSherpa, Name: filepath.Base(cfg.ASR.ModelDir), Path: cfg.ASR.ModelDir})
	}
	return res
}

func checkWhisperConfig(cfg config.Config, opts CheckOptions) CheckResult {
	path := cfg.WhisperModelPath()
	res := CheckResult{Dir: filepath.Dir(path), Engine: asr.EngineWhisper, OK: true}
	checkRequiredPaths(&res, []requiredPath{{
		Name:    cfg.ASR.WhisperModel,
		Path:    path,
		MinSize: WhisperModelMinSize(cfg.ASR.WhisperModel),
	}}, opts)
	if res.OK {
		res.Validated = append(res.Validated, CheckedModel{Engine: asr.EngineWhisper, Name: cfg.ASR.WhisperModel, Path: path})
	}
	return res
}

func checkAutoConfig(cfg config.Config, opts CheckOptions) CheckResult {
	sherpaCfg := cfg
	sherpaCfg.ASR.Engine = asr.EngineSherpa
	sherpaCfg.ASR.Provider = asr.ProviderCPU
	sherpa := checkSherpaConfig(sherpaCfg, opts)
	whisper := checkWhisperConfig(cfg, opts)
	res := CheckResult{Dir: cfg.ASR.ModelDir, Engine: asr.EngineAuto, OK: true}
	if sherpa.OK {
		mergeSuccessfulCheck(&res, sherpa)
	}
	if whisper.OK {
		mergeSuccessfulCheck(&res, whisper)
	}
	if len(res.Validated) > 0 {
		return res
	}
	res.Items = append(res.Items, sherpa.Items...)
	res.Items = append(res.Items, whisper.Items...)
	res.addErr(fmt.Sprintf("no usable ASR model; sherpa-onnx: %s; whisper-cpp: %s", strings.Join(sherpa.Errors, "; "), strings.Join(whisper.Errors, "; ")))
	return res
}

func mergeSuccessfulCheck(dst *CheckResult, src CheckResult) {
	dst.Validated = append(dst.Validated, src.Validated...)
	dst.Items = append(dst.Items, src.Items...)
	dst.Warnings = append(dst.Warnings, src.Warnings...)
}

func CheckDir(dir string, opts CheckOptions) CheckResult {
	res := CheckResult{Dir: dir, Engine: asr.EngineSherpa, OK: true}
	if st, err := os.Stat(dir); err == nil && !st.IsDir() {
		res.Items = append(res.Items, CheckItem{Path: dir, Message: "not a parakeet model directory"})
		res.addErr(fmt.Sprintf("%s is not a parakeet model directory", dir))
		return res
	}
	checkRequiredPaths(&res, canonicalRequiredPaths(dir), opts)
	if err := checkTokens(filepath.Join(dir, "tokens.txt")); err != nil {
		res.addErr(err.Error())
	}
	checkMetadataAndChecksums(&res, dir)
	if res.OK {
		res.Validated = append(res.Validated, CheckedModel{Engine: asr.EngineSherpa, Name: filepath.Base(dir), Path: dir})
	}
	return res
}

func checkRequiredPaths(res *CheckResult, files []requiredPath, opts CheckOptions) {
	for _, req := range files {
		st, err := os.Stat(req.Path)
		item := CheckItem{Path: req.Path}
		if err != nil {
			item.OK = false
			item.Message = err.Error()
			res.addErr(fmt.Sprintf("%s: %v", req.Name, err))
		} else if !st.Mode().IsRegular() {
			item.OK = false
			item.Message = "is not a regular file"
			res.addErr(req.Name + " is not a regular file")
		} else {
			item.Size = st.Size()
			item.OK = true
			if err := checkReadable(req.Path); err != nil {
				item.OK = false
				item.Message = err.Error()
				res.addErr(fmt.Sprintf("%s is not readable: %v", req.Name, err))
			}
			if opts.StrictSizes && st.Size() < req.MinSize {
				item.OK = false
				item.Message = fmt.Sprintf("size %d is below plausible minimum %d", st.Size(), req.MinSize)
				res.addErr(req.Name + " size is implausibly small")
			}
		}
		res.Items = append(res.Items, item)
	}
}

func checkMetadataAndChecksums(res *CheckResult, dir string) {
	for _, name := range MetadataFiles() {
		if err := checkOptionalMetadata(filepath.Join(dir, name)); err != nil {
			res.addWarning(fmt.Sprintf("%s: %v", name, err))
		}
	}
	if err := verifyChecksums(dir); err != nil {
		res.addErr(err.Error())
	}
}

func canonicalRequiredPaths(dir string) []requiredPath {
	required := requiredFilesForDir(dir)
	out := make([]requiredPath, 0, len(required))
	for _, req := range required {
		out = append(out, requiredPath{
			Name:    req.Name,
			Path:    filepath.Join(dir, req.Name),
			MinSize: req.MinSize,
		})
	}
	return out
}

func configuredRequiredPaths(cfg config.Config) []requiredPath {
	out := []requiredPath{
		{Name: cfg.ASR.Encoder, Path: cfg.EncoderPath(), MinSize: configuredMinSize("encoder", cfg.ASR.Encoder)},
		{Name: cfg.ASR.Decoder, Path: cfg.DecoderPath(), MinSize: configuredMinSize("decoder", cfg.ASR.Decoder)},
		{Name: cfg.ASR.Joiner, Path: cfg.JoinerPath(), MinSize: configuredMinSize("joiner", cfg.ASR.Joiner)},
		{Name: cfg.ASR.Tokens, Path: cfg.TokensPath(), MinSize: configuredMinSize("tokens", cfg.ASR.Tokens)},
	}
	if cfg.ASR.Encoder == "encoder.onnx" {
		out = append(out, requiredPath{
			Name:    "encoder.weights",
			Path:    filepath.Join(cfg.ASR.ModelDir, "encoder.weights"),
			MinSize: configuredMinSize("encoder_weights", "encoder.weights"),
		})
	}
	return out
}

func requiredFilesForDir(dir string) []RequiredFile {
	if fileExists(filepath.Join(dir, "encoder.int8.onnx")) && !fileExists(filepath.Join(dir, "encoder.onnx")) {
		return ParakeetV3Int8Files()
	}
	return RequiredFiles()
}

func configuredMinSize(role, name string) int64 {
	for _, req := range append(ParakeetUnifiedFP32Files(), ParakeetV3Int8Files()...) {
		if req.Name == name {
			return req.MinSize
		}
	}
	if role == "tokens" {
		return 32
	}
	return 1024
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (r *CheckResult) addErr(msg string) {
	r.OK = false
	r.Errors = append(r.Errors, msg)
}

func (r *CheckResult) addWarning(msg string) {
	r.Warnings = append(r.Warnings, msg)
}

func checkTokens(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if strings.TrimSpace(string(b)) == "" {
		return fmt.Errorf("tokens.txt is empty")
	}
	return nil
}

func checkOptionalMetadata(path string) error {
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		return err
	}
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("is a directory")
	}
	return checkReadable(path)
}

func checkReadable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return f.Close()
}

func verifyChecksums(dir string) error {
	path := filepath.Join(dir, DefaultChecksumFile)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return fmt.Errorf("invalid checksum line %q", line)
		}
		want := strings.ToLower(fields[0])
		name := strings.TrimPrefix(fields[1], "*")
		if err := validateChecksumName(name); err != nil {
			return err
		}
		got, err := fileSHA256(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("checksum mismatch for %s", name)
		}
	}
	return scanner.Err()
}

func validateChecksumName(name string) error {
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("unsafe checksum path %q", name)
	}
	return nil
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

// VADCheckResult reports whether the configured VAD engine has the model it needs.
type VADCheckResult struct {
	Engine  string `json:"engine"`
	Model   string `json:"model,omitempty"`
	OK      bool   `json:"ok"`
	Warning string `json:"warning,omitempty"`
}

// CheckVADConfig verifies the model for the configured VAD engine. A missing silero
// model is not fatal — the daemon falls back to the energy engine — so it reports a
// warning with OK still true, rather than an error.
func CheckVADConfig(cfg config.Config) VADCheckResult {
	res := VADCheckResult{Engine: cfg.VAD.Engine, OK: true}
	if cfg.VAD.Engine != "silero" {
		return res
	}
	res.Model = cfg.VAD.Model
	if _, err := os.Stat(cfg.VAD.Model); err != nil {
		res.Warning = fmt.Sprintf("silero model missing at %s: the daemon falls back to the energy engine, where vad.threshold/negative_threshold are read as linear RMS instead of 0..1 probabilities — run 'waydict model install silero-vad'", cfg.VAD.Model)
	}
	return res
}
