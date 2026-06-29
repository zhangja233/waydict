package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"sway-voice/internal/app"
	"sway-voice/internal/asr"
	sherpaasr "sway-voice/internal/asr/sherpa"
	"sway-voice/internal/audio"
	"sway-voice/internal/audio/pipewire"
	"sway-voice/internal/buildinfo"
	"sway-voice/internal/config"
	"sway-voice/internal/control"
	"sway-voice/internal/exitcode"
	"sway-voice/internal/inject"
	"sway-voice/internal/model"
	"sway-voice/internal/swayipc"
	"sway-voice/pkg/api"
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
  sway-voice daemon [--config PATH] [--foreground] [--log-level LEVEL]
  sway-voice start [--mode toggle|oneshot|hold]
  sway-voice stop [--commit|--discard]
  sway-voice toggle
  sway-voice status [--json]
  sway-voice transcribe --file PATH [--inject]
  sway-voice model check [--config PATH] [--dir PATH]
  sway-voice model install parakeet-v3-int8 [--dir PATH]
  sway-voice bench --file PATH [--repeat N]
  sway-voice doctor`)
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
	if err := app.RunDaemon(ctx, cfg); err != nil {
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
		return exitcode.DaemonUnavailable
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
	var prepared *preparedInjection
	if *injectText {
		var err error
		prepared, err = prepareInjection(context.Background(), cfg)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitForErr(err)
		}
	}
	tr, code := transcribeFileFunc(context.Background(), cfg, *file, stderr)
	if code != exitcode.Success {
		return code
	}
	printText := inject.NewPostProcessor(cfg.PostProcess, false).Apply(tr.Text)
	fmt.Fprintln(stdout, strings.TrimRight(printText, " "))
	if *injectText {
		text := inject.NewPostProcessor(cfg.PostProcess, cfg.Injection.AppendSpace).Apply(tr.Text)
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
	if err := validateModelForUse(cfg); err != nil {
		fmt.Fprintln(stderr, err)
		return asr.Transcript{}, exitcode.ModelInvalid
	}
	wav, err := audio.ReadFile(file)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return asr.Transcript{}, exitcode.Generic
	}
	engine := sherpaasr.New(cfg.ASR)
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
}

type preparedInjection struct {
	injector inject.Injector
	guard    focusGuard
}

var (
	transcribeFileFunc = transcribeFile
	newInjector        = func(cfg config.Injection) inject.Injector { return inject.NewWtype(cfg) }
	newFocusGuard      = func(cfg config.Config) focusGuard {
		return swayipc.NewGuard(swayipc.New(cfg.Sway.Socket), cfg.Injection.FocusPolicy)
	}
)

func prepareInjection(ctx context.Context, cfg config.Config) (*preparedInjection, error) {
	w := newInjector(cfg.Injection)
	if err := w.Available(ctx); err != nil {
		return nil, fmt.Errorf("wtype unavailable: %w", err)
	}
	prepared := &preparedInjection{injector: w}
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
		if err := p.guard.Check(ctx); err != nil {
			return err
		}
	}
	return p.injector.TypeText(ctx, text)
}

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
		if *dir != "" {
			res = model.CheckDir(*dir, model.CheckOptions{StrictSizes: true})
		} else {
			cfg, err := config.Load(*configPath)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return exitcode.Generic
			}
			res = model.CheckConfig(cfg, model.CheckOptions{StrictSizes: true})
		}
		if *jsonOut {
			printJSON(stdout, res)
		} else {
			printModelCheck(stdout, res)
		}
		if !res.OK {
			return exitcode.ModelInvalid
		}
		return exitcode.Success
	case "install":
		if len(args) < 2 || args[1] != "parakeet-v3-int8" {
			fmt.Fprintln(stderr, "usage: sway-voice model install parakeet-v3-int8 [--dir PATH]")
			return exitcode.Usage
		}
		fs := flag.NewFlagSet("model install", flag.ContinueOnError)
		fs.SetOutput(stderr)
		dir := fs.String("dir", "", "model root")
		if err := fs.Parse(args[2:]); err != nil {
			return exitcode.Usage
		}
		ctx := context.Background()
		path, err := model.InstallParakeetV3Int8(ctx, model.InstallOptions{Dir: *dir})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitcode.Generic
		}
		fmt.Fprintln(stdout, path)
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
	if err := validateModelForUse(cfg); err != nil {
		fmt.Fprintln(stderr, err)
		return exitcode.ModelInvalid
	}
	wav, err := audio.ReadFile(*file)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitcode.Generic
	}
	engine := sherpaasr.New(cfg.ASR)
	defer engine.Close()
	if err := engine.Load(context.Background()); err != nil {
		fmt.Fprintln(stderr, err)
		return exitcode.ModelInvalid
	}
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
	out := map[string]any{
		"file":           *file,
		"audio_seconds":  tr.AudioDuration.Seconds(),
		"decode_seconds": total.Seconds() / float64(*repeat),
		"rtf":            (total.Seconds() / float64(*repeat)) / tr.AudioDuration.Seconds(),
		"threads":        cfg.ASR.NumThreads,
		"provider":       cfg.ASR.Provider,
		"model":          config.DefaultModelName,
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
	check("WAYLAND_DISPLAY", envPresent("WAYLAND_DISPLAY"))
	check("SWAYSOCK", envPresent("SWAYSOCK"))
	check("XDG_RUNTIME_DIR", envPresent("XDG_RUNTIME_DIR"))
	check("sherpa build", featureEnabled(buildinfo.SherpaEnabled, "rebuild with -tags sherpa and CGO_ENABLED=1"))
	check("PipeWire build", featureEnabled(buildinfo.PipeWireEnabled, "rebuild with -tags pipewire and libpipewire-0.3 development files"))
	check("wtype", inject.NewWtype(cfg.Injection).Available(context.Background()))
	check("PipeWire", pipewire.Check())
	focus := swayipc.New(cfg.Sway.Socket)
	fctx, cancel := context.WithTimeout(context.Background(), time.Second)
	check("Sway IPC", focus.Available(fctx))
	cancel()
	res := model.CheckConfig(cfg, model.CheckOptions{StrictSizes: true})
	if res.OK {
		fmt.Fprintf(stdout, "OK   %-18s\n", "model")
	} else {
		failures++
		fmt.Fprintf(stdout, "FAIL %-18s %s\n", "model", strings.Join(res.Errors, "; "))
	}
	fmt.Fprintf(stdout, "INFO %-18s %s/%s cgo=%s goroutines=%d\n", "go", runtime.GOOS, runtime.GOARCH, os.Getenv("CGO_ENABLED"), runtime.NumGoroutine())
	if failures > 0 {
		return exitcode.DependencyMissing
	}
	return exitcode.Success
}

func printStatus(w io.Writer, st api.Status) {
	mode := "null"
	if st.Mode != nil {
		mode = string(*st.Mode)
	}
	fmt.Fprintf(w, "state=%s mode=%s model_loaded=%t audio=%s capturing=%t overruns=%d\n", st.State, mode, st.ASR.Loaded, st.Audio.Backend, st.Audio.Capturing, st.Audio.Overruns)
	if st.LastError != nil {
		fmt.Fprintf(w, "last_error=%s: %s\n", st.LastError.Code, st.LastError.Message)
	}
}

func printModelCheck(w io.Writer, res model.CheckResult) {
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
