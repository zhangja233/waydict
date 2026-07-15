package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"waydict/internal/app"
	"waydict/internal/asr"
	sherpaasr "waydict/internal/asr/sherpa"
	"waydict/internal/audio"
	"waydict/internal/audio/pipewire"
	"waydict/internal/buildinfo"
	"waydict/internal/config"
	"waydict/internal/control"
	"waydict/internal/exitcode"
	"waydict/internal/inject"
	"waydict/internal/model"
	"waydict/internal/modelinstall"
	"waydict/internal/swayipc"
	"waydict/pkg/api"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitcode.Usage
	}
	switch args[0] {
	case "daemon":
		return runDaemon(args[1:], stderr)
	case "start":
		return sendCommand(args[1:], stdout, stderr, "start")
	case "stop":
		return sendCommand(args[1:], stdout, stderr, "stop")
	case "release":
		return sendCommand(args[1:], stdout, stderr, "release")
	case "toggle":
		return sendCommand(args[1:], stdout, stderr, "toggle")
	case "status":
		return sendCommand(args[1:], stdout, stderr, "status")
	case "transcribe":
		return runTranscribe(args[1:], stdout, stderr)
	case "model":
		return runModel(args[1:], stdout, stderr)
	case "bench":
		return runBench(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		usage(stderr)
		return exitcode.Usage
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage:
  waydict daemon [--config PATH] [--foreground] [--log-level LEVEL]
  waydict start [--mode toggle|oneshot|hold]
  waydict stop [--commit|--discard]
  waydict release
  waydict toggle
  waydict status [--json]
  waydict transcribe --file PATH [--inject]
  waydict model check [--config PATH] [--dir PATH]
  waydict model install <parakeet-unified-en-0.6b-fp32|parakeet-v3-int8|silero-vad|whisper-model-name, e.g. ggml-large-v3-turbo|all> [--dir PATH]
    any whisper.cpp ggml model name works; catalog names are integrity-pinned, others are size-checked
  waydict bench --file PATH [--repeat N]
  waydict doctor`)
}

func runDaemon(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config path")
	_ = fs.Bool("foreground", false, "run in foreground")
	logLevel := fs.String("log-level", "", "log level")
	if err := fs.Parse(args); err != nil {
		return exitcode.Usage
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitcode.Generic
	}
	if *logLevel != "" {
		cfg.Daemon.LogLevel = *logLevel
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.RunDaemonWithOptions(ctx, cfg, app.DaemonOptions{
		ConfigPath:       *configPath,
		LogLevelOverride: *logLevel,
		NewWhisper:       newWhisperEngineHook,
		ProbeGPU:         probeGPUHook,
	}); err != nil {
		fmt.Fprintln(stderr, err)
		return exitForErr(err)
	}
	return exitcode.Success
}

func sendCommand(args []string, stdout, stderr io.Writer, command string) int {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", "", "mode")
	commit := fs.Bool("commit", false, "commit buffered speech")
	discard := fs.Bool("discard", false, "discard buffered speech")
	jsonOut := fs.Bool("json", false, "print JSON status")
	if err := fs.Parse(args); err != nil {
		return exitcode.Usage
	}
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitcode.Generic
	}
	reqArgs := map[string]any{}
	switch command {
	case "start":
		if *mode != "" {
			if *mode != "toggle" && *mode != "oneshot" && *mode != "hold" {
				fmt.Fprintln(stderr, "mode must be toggle, oneshot, or hold")
				return exitcode.Usage
			}
			reqArgs["mode"] = *mode
		}
	case "stop":
		if *commit && *discard {
			fmt.Fprintln(stderr, "stop accepts only one of --commit or --discard")
			return exitcode.Usage
		}
		reqArgs["commit"] = *commit
		reqArgs["discard"] = *discard
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := control.Send(ctx, cfg.Daemon.Socket, control.NewRequest(command, reqArgs))
	if err != nil {
		fmt.Fprintf(stderr, "daemon unavailable: %v\n", err)
		return exitForControlErr(err)
	}
	if command == "status" {
		if *jsonOut {
			printJSON(stdout, resp.Status)
		} else {
			printStatus(stdout, resp.Status)
		}
	}
	if !resp.OK {
		fmt.Fprintf(stderr, "%s: %s\n", resp.Error.Code, resp.Error.Message)
		return exitcode.ForErrorCode(resp.Error.Code)
	}
	return exitcode.Success
}

func exitForControlErr(err error) int {
	if errors.Is(err, control.ErrSocketPermission) {
		return exitcode.Permission
	}
	return exitcode.DaemonUnavailable
}

func runTranscribe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("transcribe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", "", "audio file")
	injectText := fs.Bool("inject", false, "inject text through wtype")
	configPath := fs.String("config", "", "config path")
	if err := fs.Parse(args); err != nil {
		return exitcode.Usage
	}
	if *file == "" {
		fmt.Fprintln(stderr, "missing --file")
		return exitcode.Usage
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitcode.Generic
	}
	if err := cfg.ValidateASR(); err != nil {
		fmt.Fprintln(stderr, err)
		return exitcode.ModelInvalid
	}
	var prepared *preparedInjection
	if *injectText {
		var err error
		prepared, err = prepareInjection(context.Background(), cfg, stderr)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitForErr(err)
		}
	}
	tr, code := transcribeFileFunc(context.Background(), cfg, *file, stderr)
	if code != exitcode.Success {
		return code
	}
	printText, _ := inject.NewPostProcessor(cfg.PostProcess, false).Apply(tr.Text, inject.CaseState{AtBoundary: true})
	fmt.Fprintln(stdout, strings.TrimRight(printText, " "))
	if *injectText {
		text, _ := inject.NewPostProcessor(cfg.PostProcess, cfg.Injection.AppendSpace).Apply(tr.Text, inject.CaseState{AtBoundary: true})
		if text == "" {
			return exitcode.Success
		}
		if err := prepared.TypeText(context.Background(), text); err != nil {
			fmt.Fprintln(stderr, err)
			return exitForErr(err)
		}
	}
	return exitcode.Success
}

func transcribeFile(ctx context.Context, cfg config.Config, file string, stderr io.Writer) (asr.Transcript, int) {
	engine, resolution, err := resolveASREngine(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return asr.Transcript{}, exitcode.ModelInvalid
	}
	if err := validateResolvedASRForUse(cfg, resolution); err != nil {
		_ = engine.Close()
		fmt.Fprintln(stderr, err)
		return asr.Transcript{}, exitcode.ModelInvalid
	}
	wav, err := readAudioFileFunc(file)
	if err != nil {
		_ = engine.Close()
		fmt.Fprintln(stderr, err)
		return asr.Transcript{}, exitcode.Generic
	}
	engine, resolution, err = loadResolvedASR(ctx, cfg, engine, resolution, stderr)
	if err != nil {
		_ = engine.Close()
		fmt.Fprintln(stderr, err)
		return asr.Transcript{}, exitcode.ModelInvalid
	}
	defer engine.Close()
	seg := asr.AudioSegment{
		ID:         "file",
		Samples:    wav.Samples,
		SampleRate: wav.SampleRate,
		StartedAt:  time.Now(),
		Duration:   wav.Duration,
	}
	tr, err := engine.Transcribe(ctx, seg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return asr.Transcript{}, exitcode.RecognitionFailed
	}
	return tr, exitcode.Success
}

type focusGuard interface {
	CaptureStart(context.Context) error
	Check(context.Context) error
	CheckWithWarning(context.Context) (*swayipc.FocusChange, error)
}

type preparedInjection struct {
	injector inject.Injector
	guard    focusGuard
	stderr   io.Writer
}

var (
	transcribeFileFunc = transcribeFile
	newInjector        = func(cfg config.Injection) inject.Injector { return inject.NewWtype(cfg) }
	newFocusGuard      = func(cfg config.Config) focusGuard {
		return swayipc.NewGuard(swayipc.New(cfg.Sway.Socket), cfg.Injection.FocusPolicy)
	}
	readAudioFileFunc     = audio.ReadFile
	newASREngine          = func(cfg config.ASR) asr.Engine { return sherpaasr.New(cfg) }
	validateModelForUseFn = validateModelForUse
	newWhisperEngineHook  func(modelPath string, device, threads int, useGPU bool) (asr.Engine, error)
	probeGPUHook          func() (string, error)
)

func resolveASREngine(cfg config.Config) (asr.Engine, asr.Resolution, error) {
	provider := cfg.ASR.Provider
	if provider == "" && cfg.ASR.Engine == asr.EngineWhisper {
		provider = asr.ProviderVulkan
	}
	sherpaCfg := cfg.ASR
	sherpaCfg.Provider = asr.ProviderCPU
	deps := asr.ResolverDeps{
		NewSherpa: func() asr.Engine { return newASREngine(sherpaCfg) },
		ProbeGPU:  probeGPUHook,
		WhisperModelPath: func() (string, error) {
			path := cfg.WhisperModelPath()
			info, err := os.Stat(path)
			if err != nil {
				return "", err
			}
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("%s is not a regular file", path)
			}
			return path, nil
		},
	}
	if hook := newWhisperEngineHook; hook != nil {
		deps.NewWhisper = func(modelPath string, device int, useGPU bool) (asr.Engine, error) {
			return hook(modelPath, device, cfg.ASR.NumThreads, useGPU)
		}
	}
	engine, resolution, err := asr.Resolve(cfg.ASR.Engine, provider, cfg.ASR.GPUDevice, deps)
	if err != nil {
		return nil, asr.Resolution{}, err
	}
	if resolution.Engine == asr.EngineSherpa {
		if err := validateResolvedSherpaConfig(cfg); err != nil {
			_ = engine.Close()
			return nil, resolution, err
		}
	}
	return engine, resolution, nil
}

func loadResolvedASR(ctx context.Context, cfg config.Config, engine asr.Engine, resolution asr.Resolution, stderr io.Writer) (asr.Engine, asr.Resolution, error) {
	if err := engine.Load(ctx); err != nil {
		if cfg.ASR.Engine != asr.EngineAuto || resolution.Engine != asr.EngineWhisper {
			return engine, resolution, err
		}
		fmt.Fprintf(stderr, "whisper-cpp load failed: %v\n", err)
		_ = engine.Close()
		fallbackCfg := cfg
		fallbackCfg.ASR.Engine = asr.EngineSherpa
		fallbackCfg.ASR.Provider = asr.ProviderCPU
		fallback, fallbackResolution, fallbackErr := resolveASREngine(fallbackCfg)
		if fallbackErr != nil {
			return engine, resolution, fmt.Errorf("sherpa fallback resolution failed: %w", fallbackErr)
		}
		fallbackResolution.FallbackReason = fmt.Sprintf("whisper-cpp load failed: %v", err)
		fmt.Fprintf(stderr, "falling back to sherpa-onnx: %s\n", fallbackResolution.FallbackReason)
		engine = fallback
		resolution = fallbackResolution
		if configErr := validateResolvedSherpaConfig(fallbackCfg); configErr != nil {
			return engine, resolution, configErr
		}
		if checkErr := validateModelForUseFn(fallbackCfg); checkErr != nil {
			return engine, resolution, checkErr
		}
		if fallbackErr = engine.Load(ctx); fallbackErr != nil {
			return engine, resolution, fmt.Errorf("sherpa fallback load failed: %w", fallbackErr)
		}
	}
	resolution = confirmResolvedBackend(engine, resolution, stderr)
	return engine, resolution, nil
}

func validateResolvedASRForUse(cfg config.Config, resolution asr.Resolution) error {
	if resolution.Engine != asr.EngineSherpa {
		return nil
	}
	if err := validateResolvedSherpaConfig(cfg); err != nil {
		return err
	}
	// Pin the copy so the model check validates the resolved engine, not auto.
	cfg.ASR.Engine = asr.EngineSherpa
	cfg.ASR.Provider = asr.ProviderCPU
	return validateModelForUseFn(cfg)
}

func validateResolvedSherpaConfig(cfg config.Config) error {
	cfg.ASR.Engine = asr.EngineSherpa
	cfg.ASR.Provider = asr.ProviderCPU
	return cfg.ValidateASR()
}

func confirmResolvedBackend(engine asr.Engine, resolution asr.Resolution, stderr io.Writer) asr.Resolution {
	if resolution.Engine != asr.EngineWhisper {
		return resolution
	}
	reporter, ok := engine.(asr.BackendReporter)
	name, gpu := "", false
	if ok {
		name, gpu = reporter.ActiveBackend()
	}
	if gpu {
		resolution.Provider = asr.ProviderVulkan
		resolution.GPUName = name
	} else if resolution.Provider == asr.ProviderVulkan {
		fmt.Fprintf(stderr, "whisper-cpp backend downgraded to cpu (reported backend %q)\n", name)
		resolution.Provider = asr.ProviderCPU
		resolution.GPUName = ""
	}
	return resolution
}

func prepareInjection(ctx context.Context, cfg config.Config, stderr io.Writer) (*preparedInjection, error) {
	w := newInjector(cfg.Injection)
	if err := w.Available(ctx); err != nil {
		return nil, fmt.Errorf("wtype unavailable: %w", err)
	}
	prepared := &preparedInjection{injector: w, stderr: stderr}
	if cfg.Sway.FocusCheck {
		guard := newFocusGuard(cfg)
		if err := guard.CaptureStart(ctx); err != nil {
			return nil, err
		}
		prepared.guard = guard
	}
	return prepared, nil
}

func (p *preparedInjection) TypeText(ctx context.Context, text string) error {
	if p.guard != nil {
		warning, err := p.guard.CheckWithWarning(ctx)
		if err != nil {
			return err
		}
		if warning != nil && p.stderr != nil {
			fmt.Fprintf(p.stderr, "warning: %s\n", warning.Error())
		}
	}
	return p.injector.TypeText(ctx, text)
}

var (
	installParakeetUnifiedFP32 = modelinstall.InstallParakeetUnifiedFP32
	installParakeetV3Int8      = modelinstall.InstallParakeetV3Int8
	installSileroVAD           = modelinstall.InstallSileroVAD
	installWhisper             = modelinstall.InstallWhisper
)

func runModel(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "missing model subcommand")
		return exitcode.Usage
	}
	switch args[0] {
	case "check":
		fs := flag.NewFlagSet("model check", flag.ContinueOnError)
		fs.SetOutput(stderr)
		configPath := fs.String("config", "", "config path")
		dir := fs.String("dir", "", "model dir")
		jsonOut := fs.Bool("json", false, "JSON output")
		if err := fs.Parse(args[1:]); err != nil {
			return exitcode.Usage
		}
		var res model.CheckResult
		var vad *model.VADCheckResult
		if *dir != "" {
			res = model.CheckDir(*dir, model.CheckOptions{StrictSizes: true})
		} else {
			cfg, err := config.Load(*configPath)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return exitcode.Generic
			}
			res = model.CheckConfig(cfg, model.CheckOptions{StrictSizes: true})
			v := model.CheckVADConfig(cfg)
			vad = &v
		}
		if *jsonOut {
			if vad != nil {
				printJSON(stdout, struct {
					model.CheckResult
					VAD model.VADCheckResult `json:"vad"`
				}{res, *vad})
			} else {
				printJSON(stdout, res)
			}
		} else {
			printModelCheck(stdout, res)
			if vad != nil {
				printVADCheck(stdout, *vad)
			}
		}
		if !res.OK {
			return exitcode.ModelInvalid
		}
		return exitcode.Success
	case "install":
		const installUsage = `usage: waydict model install <parakeet-unified-en-0.6b-fp32|parakeet-v3-int8|silero-vad|whisper-model-name, e.g. ggml-large-v3-turbo|all> [--dir PATH]
any whisper.cpp ggml model name works; catalog names are integrity-pinned, others are size-checked`
		if len(args) < 2 {
			fmt.Fprintln(stderr, installUsage)
			return exitcode.Usage
		}
		name := args[1]
		if name != model.ParakeetUnifiedFP32ID && name != "parakeet-v3-int8" && name != "silero-vad" && name != "all" {
			if _, err := model.WhisperAssetForName(name); err != nil {
				fmt.Fprintln(stderr, err)
				fmt.Fprintln(stderr, installUsage)
				return exitcode.Usage
			}
		}
		fs := flag.NewFlagSet("model install", flag.ContinueOnError)
		fs.SetOutput(stderr)
		dir := fs.String("dir", "", "model root")
		if err := fs.Parse(args[2:]); err != nil {
			return exitcode.Usage
		}
		ctx := context.Background()
		install := func(kind string) bool {
			var (
				path string
				err  error
			)
			switch kind {
			case model.ParakeetUnifiedFP32ID:
				path, err = installParakeetUnifiedFP32(ctx, modelinstall.InstallOptions{Dir: *dir})
			case "parakeet-v3-int8":
				path, err = installParakeetV3Int8(ctx, modelinstall.InstallOptions{Dir: *dir})
			case "silero-vad":
				path, err = installSileroVAD(ctx, modelinstall.InstallOptions{Dir: *dir})
			default:
				path, err = installWhisper(ctx, kind, modelinstall.InstallOptions{Dir: *dir})
			}
			if err != nil {
				fmt.Fprintf(stderr, "%s: %v\n", kind, err)
				return false
			}
			fmt.Fprintln(stdout, path)
			return true
		}
		ok := true
		if name == "all" {
			ok = install(model.ParakeetUnifiedFP32ID) && ok
			ok = install("silero-vad") && ok
			ok = install(config.Defaults().ASR.WhisperModel) && ok
		} else {
			ok = install(name)
		}
		if !ok {
			return exitcode.Generic
		}
		return exitcode.Success
	default:
		fmt.Fprintf(stderr, "unknown model subcommand %q\n", args[0])
		return exitcode.Usage
	}
}

func runBench(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", "", "audio file")
	repeat := fs.Int("repeat", 1, "repeat count")
	configPath := fs.String("config", "", "config path")
	if err := fs.Parse(args); err != nil {
		return exitcode.Usage
	}
	if *file == "" {
		fmt.Fprintln(stderr, "missing --file")
		return exitcode.Usage
	}
	if *repeat < 1 {
		*repeat = 1
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitcode.Generic
	}
	if err := cfg.ValidateASR(); err != nil {
		fmt.Fprintln(stderr, err)
		return exitcode.ModelInvalid
	}
	engine, resolution, err := resolveASREngine(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitcode.ModelInvalid
	}
	if err := validateResolvedASRForUse(cfg, resolution); err != nil {
		_ = engine.Close()
		fmt.Fprintln(stderr, err)
		return exitcode.ModelInvalid
	}
	wav, err := readAudioFileFunc(*file)
	if err != nil {
		_ = engine.Close()
		fmt.Fprintln(stderr, err)
		return exitcode.Generic
	}
	if len(wav.Samples) == 0 || wav.Duration <= 0 {
		_ = engine.Close()
		fmt.Fprintln(stderr, "audio file has no samples")
		return exitcode.Generic
	}
	engine, resolution, err = loadResolvedASR(context.Background(), cfg, engine, resolution, stderr)
	if err != nil {
		_ = engine.Close()
		fmt.Fprintln(stderr, err)
		return exitcode.ModelInvalid
	}
	defer engine.Close()
	seg := asr.AudioSegment{
		ID:         "bench",
		Samples:    wav.Samples,
		SampleRate: wav.SampleRate,
		StartedAt:  time.Now(),
		Duration:   wav.Duration,
	}
	start := time.Now()
	var tr asr.Transcript
	for i := 0; i < *repeat; i++ {
		tr, err = engine.Transcribe(context.Background(), seg)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitcode.RecognitionFailed
		}
	}
	total := time.Since(start)
	audioDuration := tr.AudioDuration
	if audioDuration <= 0 {
		audioDuration = wav.Duration
	}
	if audioDuration <= 0 {
		fmt.Fprintln(stderr, "audio duration is zero")
		return exitcode.Generic
	}
	decodeSeconds := total.Seconds() / float64(*repeat)
	benchModel := filepath.Base(cfg.ASR.ModelDir)
	if resolution.Engine == asr.EngineWhisper {
		benchModel = cfg.ASR.WhisperModel
	}
	out := map[string]any{
		"file":           *file,
		"audio_seconds":  audioDuration.Seconds(),
		"decode_seconds": decodeSeconds,
		"rtf":            decodeSeconds / audioDuration.Seconds(),
		"threads":        cfg.ASR.NumThreads,
		"engine":         resolution.Engine,
		"provider":       resolution.Provider,
		"model":          benchModel,
		"rss_peak_bytes": peakRSS(),
	}
	printJSON(stdout, out)
	return exitcode.Success
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config path")
	if err := fs.Parse(args); err != nil {
		return exitcode.Usage
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitcode.Generic
	}
	failures := 0
	check := func(name string, err error) {
		if err != nil {
			failures++
			fmt.Fprintf(stdout, "FAIL %-18s %v\n", name, err)
		} else {
			fmt.Fprintf(stdout, "OK   %-18s\n", name)
		}
	}
	check("config", cfg.Validate())
	fmt.Fprintf(stdout, "INFO %-18s engine=%s provider=%s\n", "asr configured", cfg.ASR.Engine, cfg.ASR.Provider)
	doctorEngine, resolution, resolutionErr := resolveASREngine(cfg)
	if resolutionErr != nil {
		failures++
		fmt.Fprintf(stdout, "FAIL %-18s %v\n", "asr resolution", resolutionErr)
	} else if resolution.FallbackReason != "" {
		fmt.Fprintf(stdout, "WARN %-18s engine=%s provider=%s reason=%s\n", "asr resolution", resolution.Engine, resolution.Provider, resolution.FallbackReason)
	} else {
		fmt.Fprintf(stdout, "OK   %-18s engine=%s provider=%s\n", "asr resolution", resolution.Engine, resolution.Provider)
	}
	if resolutionErr == nil {
		defer doctorEngine.Close()
	}
	printVulkanICDHint(stdout)
	check("WAYLAND_DISPLAY", envPresent("WAYLAND_DISPLAY"))
	check("SWAYSOCK", envPresent("SWAYSOCK"))
	check("XDG_RUNTIME_DIR", envPresent("XDG_RUNTIME_DIR"))
	if resolutionErr == nil && resolution.Engine == asr.EngineSherpa {
		check("sherpa build", featureEnabled(buildinfo.SherpaEnabled, "rebuild with -tags sherpa and CGO_ENABLED=1"))
	}
	check("PipeWire build", featureEnabled(buildinfo.PipeWireEnabled, "rebuild with -tags pipewire and libpipewire-0.3 development files"))
	check("wtype", inject.NewWtype(cfg.Injection).Available(context.Background()))
	check("PipeWire", pipewire.Check())
	focus := swayipc.New(cfg.Sway.Socket)
	fctx, cancel := context.WithTimeout(context.Background(), time.Second)
	check("Sway IPC", focus.Available(fctx))
	cancel()
	if resolutionErr == nil {
		modelCfg := cfg
		modelCfg.ASR.Engine = resolution.Engine
		modelCfg.ASR.Provider = resolution.Provider
		res := model.CheckConfig(modelCfg, model.CheckOptions{StrictSizes: true})
		if !printDoctorModel(stdout, res) {
			failures++
		}
	}
	printDoctorVAD(stdout, model.CheckVADConfig(cfg))
	fmt.Fprintf(stdout, "INFO %-18s %s/%s cgo=%s goroutines=%d\n", "go", runtime.GOOS, runtime.GOARCH, os.Getenv("CGO_ENABLED"), runtime.NumGoroutine())
	if failures > 0 {
		return exitcode.DependencyMissing
	}
	return exitcode.Success
}

func printDoctorModel(w io.Writer, res model.CheckResult) bool {
	if res.OK {
		fmt.Fprintf(w, "OK   %-18s\n", "model")
	} else {
		fmt.Fprintf(w, "FAIL %-18s %s\n", "model", strings.Join(res.Errors, "; "))
	}
	for _, warning := range res.Warnings {
		fmt.Fprintf(w, "WARN %-18s %s\n", "model", warning)
	}
	return res.OK
}

func printDoctorVAD(w io.Writer, res model.VADCheckResult) {
	switch {
	case res.Warning != "":
		fmt.Fprintf(w, "WARN %-18s %s\n", "vad model", res.Warning)
	case res.Engine == "silero":
		fmt.Fprintf(w, "OK   %-18s engine=silero %s\n", "vad model", res.Model)
	default:
		fmt.Fprintf(w, "OK   %-18s engine=%s (no model needed)\n", "vad model", res.Engine)
	}
}

func printStatus(w io.Writer, st api.Status) {
	mode := "null"
	if st.Mode != nil {
		mode = string(*st.Mode)
	}
	fmt.Fprintf(w, "state=%s mode=%s model_loaded=%t vad=%s audio=%s capturing=%t overruns=%d\n", st.State, mode, st.ASR.Loaded, st.VAD.Engine, st.Audio.Backend, st.Audio.Capturing, st.Audio.Overruns)
	gpu := st.ASR.GPUName
	if gpu == "" {
		gpu = "none"
	}
	engine := st.ASR.ResolvedEngine
	if engine == "" {
		engine = st.ASR.Engine
	}
	provider := st.ASR.ResolvedProvider
	if provider == "" {
		provider = st.ASR.Provider
	}
	fmt.Fprintf(w, "asr=%s provider=%s gpu=%s configured=%s/%s\n", engine, provider, gpu, st.ASR.Engine, st.ASR.Provider)
	if st.ASR.FallbackReason != "" {
		fmt.Fprintf(w, "asr_fallback=%s\n", st.ASR.FallbackReason)
	}
	if st.LastError != nil {
		fmt.Fprintf(w, "last_error=%s: %s\n", st.LastError.Code, st.LastError.Message)
	}
	if st.LastWarning != nil {
		fmt.Fprintf(w, "last_warning=%s: %s\n", st.LastWarning.Code, st.LastWarning.Message)
	}
}

func printVulkanICDHint(w io.Writer) {
	for _, dir := range []string{"/run/opengl-driver/share/vulkan/icd.d", "/usr/share/vulkan/icd.d"} {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			fmt.Fprintf(w, "INFO %-18s found %s\n", "Vulkan ICD", dir)
			return
		}
	}
	fmt.Fprintf(w, "INFO %-18s no ICD directory found; install a Vulkan driver if GPU ASR is desired\n", "Vulkan ICD")
}

func printModelCheck(w io.Writer, res model.CheckResult) {
	if res.Engine != "" {
		fmt.Fprintf(w, "INFO engine=%s\n", res.Engine)
	}
	for _, checked := range res.Validated {
		fmt.Fprintf(w, "OK   engine-model %s %s %s\n", checked.Engine, checked.Name, checked.Path)
	}
	for _, item := range res.Items {
		prefix := "OK"
		if !item.OK {
			prefix = "FAIL"
		}
		fmt.Fprintf(w, "%-4s %s", prefix, item.Path)
		if item.Size > 0 {
			fmt.Fprintf(w, " (%d bytes)", item.Size)
		}
		if item.Message != "" {
			fmt.Fprintf(w, " - %s", item.Message)
		}
		fmt.Fprintln(w)
	}
	if len(res.Errors) > 0 {
		fmt.Fprintln(w, strings.Join(res.Errors, "\n"))
	}
	for _, warning := range res.Warnings {
		fmt.Fprintf(w, "WARN %s\n", warning)
	}
}

func printVADCheck(w io.Writer, res model.VADCheckResult) {
	switch {
	case res.Warning != "":
		fmt.Fprintf(w, "WARN vad %s\n", res.Warning)
	case res.Engine == "silero":
		fmt.Fprintf(w, "OK   vad %s\n", res.Model)
	default:
		fmt.Fprintf(w, "OK   vad engine=%s (no model needed)\n", res.Engine)
	}
}

func printJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func validateModelForUse(cfg config.Config) error {
	res := model.CheckConfig(cfg, model.CheckOptions{StrictSizes: true})
	if !res.OK {
		return fmt.Errorf("model validation failed: %s", strings.Join(res.Errors, "; "))
	}
	return nil
}

func envPresent(name string) error {
	if os.Getenv(name) == "" {
		return fmt.Errorf("%s is not set", name)
	}
	return nil
}

func featureEnabled(enabled bool, message string) error {
	if !enabled {
		return fmt.Errorf(message)
	}
	return nil
}

func exitForErr(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "model"):
		return exitcode.ModelInvalid
	case strings.Contains(msg, "pipewire"):
		return exitcode.PipeWireUnavailable
	case strings.Contains(msg, "sway"), strings.Contains(msg, "SWAYSOCK"), strings.Contains(msg, "focus"):
		return exitcode.SwayUnavailable
	case strings.Contains(msg, "wtype"):
		return exitcode.WtypeUnavailable
	default:
		return exitcode.Generic
	}
}

func peakRSS() uint64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "VmHWM:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseUint(fields[1], 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}
