package main

import (
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
	"strings"
	"syscall"
	"time"

	"waydict/internal/app"
	"waydict/internal/apperr"
	"waydict/internal/asr"
	sherpaasr "waydict/internal/asr/sherpa"
	"waydict/internal/audio"
	"waydict/internal/buildinfo"
	"waydict/internal/config"
	"waydict/internal/control"
	"waydict/internal/doctor"
	"waydict/internal/exitcode"
	"waydict/internal/focus"
	swayfocus "waydict/internal/focus/sway"
	"waydict/internal/inject"
	"waydict/internal/metrics"
	"waydict/internal/model"
	"waydict/internal/modelinstall"
	"waydict/internal/swayipc"
	"waydict/pkg/api"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type cliOptions struct {
	noLaunch bool
}

type commandClass string

const (
	commandClient          commandClass = "client"
	commandLocalOnly       commandClass = "local_only"
	commandDaemonDependent commandClass = "daemon_dependent"
)

const waydictBundleID = "io.github.zhangja233.waydict"

var (
	cliPlatform      = runtime.GOOS
	controlSend      = control.Send
	launchAppBundle  = launchWaydictBundle
	appLaunchTimeout = 5 * time.Second
	appPollInterval  = 50 * time.Millisecond
)

func run(args []string, stdout, stderr io.Writer) int {
	opts, args, err := parseGlobalOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		usage(stderr)
		return exitcode.Usage
	}
	if len(args) == 0 {
		usage(stderr)
		return exitcode.Usage
	}
	switch args[0] {
	case "daemon":
		return runDaemon(args[1:], stderr, opts)
	case "start":
		return sendCommand(args[1:], stdout, stderr, "start", opts)
	case "stop":
		return sendCommand(args[1:], stdout, stderr, "stop", opts)
	case "release":
		return sendCommand(args[1:], stdout, stderr, "release", opts)
	case "toggle":
		return sendCommand(args[1:], stdout, stderr, "toggle", opts)
	case "status":
		return sendCommand(args[1:], stdout, stderr, "status", opts)
	case "reload_config":
		return sendCommand(args[1:], stdout, stderr, "reload_config", opts)
	case "shutdown":
		return sendCommand(args[1:], stdout, stderr, "shutdown", opts)
	case "transcribe":
		return runTranscribe(args[1:], stdout, stderr, opts)
	case "model":
		return runModel(args[1:], stdout, stderr)
	case "bench":
		return runBench(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "permission", "permissions":
		return runPermissions(args[1:], stdout, stderr, opts)
	case "audio":
		return runAudio(args[1:], stdout, stderr, opts)
	case "restart":
		return sendCommand(args[1:], stdout, stderr, "restart_runtime", opts)
	case "app":
		return runAppCommand(args[1:], stdout, stderr, opts)
	case "diagnostics":
		return runDiagnostics(args[1:], stdout, stderr, opts)
	case "version":
		return runVersion(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		usage(stderr)
		return exitcode.Usage
	}
}

func parseGlobalOptions(args []string) (cliOptions, []string, error) {
	opts := cliOptions{}
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--no-launch":
			opts.noLaunch = true
			args = args[1:]
		default:
			return cliOptions{}, nil, fmt.Errorf("unknown global option %q", args[0])
		}
	}
	return opts, args, nil
}

func commandClassFor(platform string, args []string) commandClass {
	if len(args) == 0 {
		return commandLocalOnly
	}
	switch args[0] {
	case "model", "bench", "doctor", "version":
		return commandLocalOnly
	case "transcribe":
		for _, arg := range args[1:] {
			if arg == "--inject" || arg == "--inject=true" {
				if platform == "darwin" {
					return commandDaemonDependent
				}
				return commandLocalOnly
			}
		}
		return commandLocalOnly
	case "daemon":
		if platform == "darwin" {
			return commandDaemonDependent
		}
		return commandLocalOnly
	case "start", "stop", "release", "toggle", "status", "reload_config", "shutdown", "permission", "permissions", "audio", "restart", "app", "diagnostics":
		if platform == "darwin" {
			return commandDaemonDependent
		}
		return commandClient
	default:
		return commandLocalOnly
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage:
	waydict [--no-launch] COMMAND
  waydict daemon [--config PATH] [--foreground] [--log-level LEVEL]
  waydict start [--mode toggle|oneshot|hold]
  waydict stop [--commit|--discard]
  waydict release
  waydict toggle
  waydict status [--json]
	waydict reload_config
	waydict shutdown
	waydict app open|quit|status|restart|install|cancel-install|diagnostics
	waydict permissions [--json]
	waydict audio devices [--json]
	waydict audio use <device-uid|default>
	waydict restart
	waydict diagnostics [--json]
	waydict version [--json]
  waydict transcribe --file PATH [--inject]
  waydict model check [--config PATH] [--dir PATH]
  waydict model install <parakeet-unified-en-0.6b-fp32|parakeet-v3-int8|silero-vad|whisper-model-name, e.g. ggml-large-v3-turbo|all> [--dir PATH]
    any whisper.cpp ggml model name works; catalog names are integrity-pinned, others are size-checked
  waydict bench --file PATH [--repeat N]
  waydict doctor`)
}

type versionOutput struct {
	Version            string `json:"version"`
	BuildNumber        string `json:"build_number"`
	Commit             string `json:"commit"`
	BuildTags          string `json:"build_tags"`
	GoVersion          string `json:"go_version"`
	GOOS               string `json:"goos"`
	GOARCH             string `json:"goarch"`
	ProtocolVersion    int    `json:"protocol_version"`
	XcodeVersion       string `json:"xcode_version"`
	SDKVersion         string `json:"sdk_version"`
	DeploymentTarget   string `json:"deployment_target"`
	WhisperCommit      string `json:"whisper_commit"`
	SherpaVersion      string `json:"sherpa_version"`
	ONNXRuntimeVersion string `json:"onnxruntime_version"`
	ModelCatalogSHA256 string `json:"model_catalog_sha256"`
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "print JSON build metadata")
	if err := fs.Parse(args); err != nil {
		return exitcode.Usage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "version accepts no positional arguments")
		return exitcode.Usage
	}
	info := versionOutput{
		Version:            buildinfo.Version,
		BuildNumber:        buildinfo.BuildNumber,
		Commit:             buildinfo.Commit,
		BuildTags:          buildinfo.BuildTags,
		GoVersion:          runtime.Version(),
		GOOS:               runtime.GOOS,
		GOARCH:             runtime.GOARCH,
		ProtocolVersion:    control.Version,
		XcodeVersion:       buildinfo.XcodeVersion,
		SDKVersion:         buildinfo.SDKVersion,
		DeploymentTarget:   buildinfo.DeploymentTarget,
		WhisperCommit:      buildinfo.WhisperCommit,
		SherpaVersion:      buildinfo.SherpaVersion,
		ONNXRuntimeVersion: buildinfo.ONNXRuntimeVersion,
		ModelCatalogSHA256: buildinfo.ModelCatalogSHA256,
	}
	if *jsonOut {
		printJSON(stdout, info)
	} else {
		fmt.Fprintf(stdout, "waydict %s (build %s, commit %s)\n", info.Version, info.BuildNumber, info.Commit)
	}
	return exitcode.Success
}

func runDaemon(args []string, stderr io.Writer, opts cliOptions) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config path")
	foreground := fs.Bool("foreground", false, "run in foreground")
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
	if cliPlatform == "darwin" {
		fmt.Fprintln(stderr, "waydict daemon: the signed Waydict app hosts the daemon on macOS")
		ctx, cancel := context.WithTimeout(context.Background(), appLaunchTimeout)
		resp, err := sendRuntimeRequest(ctx, cfg.Daemon.Socket, control.NewRequest("status", nil), opts)
		cancel()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitForControlErr(err)
		}
		if !resp.OK {
			fmt.Fprintf(stderr, "%s: %s\n", resp.Error.Code, resp.Error.Message)
			return exitcode.ForErrorCode(resp.Error.Code)
		}
		if !*foreground {
			return exitcode.Success
		}
		for {
			time.Sleep(250 * time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_, err := controlSend(ctx, cfg.Daemon.Socket, control.NewRequest("status", nil))
			cancel()
			if err != nil {
				return exitcode.Success
			}
		}
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

func sendCommand(args []string, stdout, stderr io.Writer, command string, opts cliOptions) int {
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
	ctx, cancel := context.WithTimeout(context.Background(), appLaunchTimeout+time.Second)
	defer cancel()
	resp, err := sendRuntimeRequest(ctx, cfg.Daemon.Socket, control.NewRequest(command, reqArgs), opts)
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

func sendRuntimeRequest(ctx context.Context, socket string, req control.Request, opts cliOptions) (control.Response, error) {
	resp, err := controlSend(ctx, socket, req)
	if err == nil || cliPlatform != "darwin" || opts.noLaunch || !launchableSocketError(err) {
		return resp, err
	}
	if launchErr := launchAppBundle(ctx, waydictBundleID); launchErr != nil {
		return control.Response{}, apperr.New(apperr.CodeAppNotInstalled, "launch Waydict.app", fmt.Errorf("Waydict.app (%s) is not installed: %w", waydictBundleID, launchErr))
	}
	deadline := time.Now().Add(appLaunchTimeout)
	for {
		if time.Now().After(deadline) {
			return control.Response{}, fmt.Errorf("daemon unavailable after launching Waydict.app: %w", err)
		}
		select {
		case <-ctx.Done():
			return control.Response{}, ctx.Err()
		case <-time.After(appPollInterval):
		}
		resp, err = controlSend(ctx, socket, req)
		if err == nil || !launchableSocketError(err) {
			return resp, err
		}
	}
}

func launchableSocketError(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT)
}

func exitForControlErr(err error) int {
	if errors.Is(err, control.ErrSocketPermission) {
		return exitcode.Permission
	}
	if code := apperr.Code(err); code != apperr.CodeInternalError {
		return exitcode.ForErrorCode(code)
	}
	return exitcode.DaemonUnavailable
}

func runPermissions(args []string, stdout, stderr io.Writer, opts cliOptions) int {
	fs := flag.NewFlagSet("permissions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return exitcode.Usage
	}
	resp, code := requestDataCommand("permissions", nil, stdout, stderr, opts)
	if code != exitcode.Success {
		return code
	}
	if *jsonOut {
		printJSON(stdout, resp.Data)
		return exitcode.Success
	}
	for _, key := range []string{"microphone", "accessibility"} {
		fmt.Fprintf(stdout, "%s=%v\n", key, resp.Data[key])
	}
	fmt.Fprintf(stdout, "input_monitoring=%v (informational; not required)\n", resp.Data["input_monitoring"])
	return exitcode.Success
}

func runAudio(args []string, stdout, stderr io.Writer, opts cliOptions) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "missing audio subcommand")
		return exitcode.Usage
	}
	switch args[0] {
	case "devices":
		fs := flag.NewFlagSet("audio devices", flag.ContinueOnError)
		fs.SetOutput(stderr)
		jsonOut := fs.Bool("json", false, "print JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return exitcode.Usage
		}
		resp, code := requestDataCommand("list_audio_devices", nil, stdout, stderr, opts)
		if code != exitcode.Success {
			return code
		}
		if *jsonOut {
			printJSON(stdout, resp.Data["devices"])
			return exitcode.Success
		}
		printAudioDevices(stdout, resp.Data["devices"])
		return exitcode.Success
	case "use":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: waydict audio use <device-uid|default>")
			return exitcode.Usage
		}
		_, code := requestDataCommand("set_audio_device", map[string]any{"id": args[1]}, stdout, stderr, opts)
		return code
	default:
		fmt.Fprintf(stderr, "unknown audio subcommand %q\n", args[0])
		return exitcode.Usage
	}
}

func runAppCommand(args []string, stdout, stderr io.Writer, opts cliOptions) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: waydict app open|quit|status|restart|install|cancel-install|diagnostics")
		return exitcode.Usage
	}
	switch args[0] {
	case "open":
		if len(args) != 1 {
			return exitcode.Usage
		}
		_, code := requestDataCommand("activate_app", nil, stdout, stderr, opts)
		return code
	case "quit":
		if len(args) != 1 {
			return exitcode.Usage
		}
		_, code := requestDataCommand("shutdown", nil, stdout, stderr, opts)
		return code
	case "status":
		return sendCommand(args[1:], stdout, stderr, "status", opts)
	case "restart":
		return sendCommand(args[1:], stdout, stderr, "restart_runtime", opts)
	case "install":
		return runAppInstall(args[1:], stdout, stderr, opts)
	case "cancel-install":
		if len(args) != 1 {
			return exitcode.Usage
		}
		_, code := requestDataCommand("cancel_model_install", nil, stdout, stderr, opts)
		return code
	case "diagnostics":
		return runDiagnostics(args[1:], stdout, stderr, opts)
	default:
		fmt.Fprintln(stderr, "usage: waydict app open|quit|status|restart|install|cancel-install|diagnostics")
		return exitcode.Usage
	}
}

func runAppInstall(args []string, stdout, stderr io.Writer, opts cliOptions) int {
	fs := flag.NewFlagSet("app install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "print final JSON status")
	if err := fs.Parse(args); err != nil {
		return exitcode.Usage
	}
	if fs.NArg() != 0 {
		return exitcode.Usage
	}
	response, code := requestDataCommand("install_required_models", nil, stdout, stderr, opts)
	if code != exitcode.Success {
		return code
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	last := ""
	for {
		status := response.Status.ModelInstall
		if status != nil {
			value := fmt.Sprintf("%s %s %.0f%%", status.Item, status.Phase, status.Percent)
			if value != last && status.Running {
				fmt.Fprintln(stderr, strings.TrimSpace(value))
				last = value
			}
			if !status.Running {
				if *jsonOut {
					printJSON(stdout, status)
				} else {
					fmt.Fprintf(stdout, "model_install=%s\n", status.Phase)
				}
				if status.Error != nil {
					fmt.Fprintf(stderr, "%s: %s\n", status.Error.Code, status.Error.Message)
					return exitcode.ForErrorCode(status.Error.Code)
				}
				return exitcode.Success
			}
		}
		select {
		case <-ctx.Done():
			cancelCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			cfg, err := config.Load("")
			if err == nil {
				_, _ = sendRuntimeRequest(cancelCtx, cfg.Daemon.Socket, control.NewRequest("cancel_model_install", nil), opts)
			}
			cancel()
			fmt.Fprintln(stderr, "model installation cancellation requested")
			return exitcode.Generic
		case <-time.After(200 * time.Millisecond):
		}
		cfg, err := config.Load("")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitcode.Generic
		}
		requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		response, err = sendRuntimeRequest(requestCtx, cfg.Daemon.Socket, control.NewRequest("model_install_status", nil), opts)
		cancel()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitForControlErr(err)
		}
		if !response.OK {
			fmt.Fprintf(stderr, "%s: %s\n", response.Error.Code, response.Error.Message)
			return exitcode.ForErrorCode(response.Error.Code)
		}
	}
}

func runDiagnostics(args []string, stdout, stderr io.Writer, opts cliOptions) int {
	fs := flag.NewFlagSet("diagnostics", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return exitcode.Usage
	}
	response, code := requestDataCommand("run_diagnostics", nil, stdout, stderr, opts)
	if code != exitcode.Success {
		return code
	}
	if *jsonOut {
		printJSON(stdout, response.Data)
		return exitcode.Success
	}
	report, _ := response.Data["report"].(string)
	if report == "" {
		fmt.Fprintln(stderr, "diagnostics report is unavailable")
		return exitcode.Generic
	}
	fmt.Fprintln(stdout, report)
	return exitcode.Success
}

func requestDataCommand(command string, args map[string]any, stdout, stderr io.Writer, opts cliOptions) (control.Response, int) {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return control.Response{}, exitcode.Generic
	}
	ctx, cancel := context.WithTimeout(context.Background(), appLaunchTimeout+time.Second)
	defer cancel()
	resp, err := sendRuntimeRequest(ctx, cfg.Daemon.Socket, control.NewRequest(command, args), opts)
	if err != nil {
		fmt.Fprintf(stderr, "daemon unavailable: %v\n", err)
		return control.Response{}, exitForControlErr(err)
	}
	if !resp.OK {
		fmt.Fprintf(stderr, "%s: %s\n", resp.Error.Code, resp.Error.Message)
		return resp, exitcode.ForErrorCode(resp.Error.Code)
	}
	return resp, exitcode.Success
}

func printAudioDevices(w io.Writer, value any) {
	switch devices := value.(type) {
	case []audio.Device:
		for _, device := range devices {
			marker := ""
			if device.Default {
				marker = " default"
			}
			fmt.Fprintf(w, "%s\t%s%s\n", device.ID, device.Name, marker)
		}
	case []any:
		for _, item := range devices {
			device, _ := item.(map[string]any)
			marker := ""
			if value, _ := device["default"].(bool); value {
				marker = " default"
			}
			fmt.Fprintf(w, "%v\t%v%s\n", device["id"], device["name"], marker)
		}
	}
}

func runTranscribe(args []string, stdout, stderr io.Writer, opts cliOptions) int {
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
	if *injectText && cliPlatform != "darwin" {
		var err error
		prepared, err = prepareInjection(context.Background(), cfg, stderr)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitForErr(err)
		}
		defer prepared.Reset()
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
		if cliPlatform == "darwin" {
			ctx, cancel := context.WithTimeout(context.Background(), appLaunchTimeout+time.Second)
			resp, err := sendRuntimeRequest(ctx, cfg.Daemon.Socket, control.NewRequest("inject_text", map[string]any{"text": text}), opts)
			cancel()
			if err != nil {
				fmt.Fprintln(stderr, err)
				return exitForControlErr(err)
			}
			if !resp.OK {
				fmt.Fprintf(stderr, "%s: %s\n", resp.Error.Code, resp.Error.Message)
				return exitcode.ForErrorCode(resp.Error.Code)
			}
		} else if err := prepared.TypeText(context.Background(), text); err != nil {
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
	CaptureStart(context.Context, int) error
	ResolveForInjection(context.Context) (focus.Target, *focus.Change, error)
	Release(focus.Target)
	Validate(context.Context, focus.Target) error
	Reset()
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
		provider := swayfocus.New(swayipc.New(cfg.Focus.Socket))
		return &focusGuardAdapter{
			guard:    focus.NewGuard(provider, focus.Policy(cfg.EffectiveFocusPolicy())),
			provider: provider,
		}
	}
	readAudioFileFunc     = audio.ReadFile
	newASREngine          = func(cfg config.ASR) asr.Engine { return sherpaasr.New(cfg) }
	validateModelForUseFn = validateModelForUse
	newWhisperEngineHook  func(modelPath string, device, threads int, useGPU bool) (asr.Engine, error)
	probeGPUHook          func() (string, error)
)

func resolveASREngine(cfg config.Config) (asr.Engine, asr.Resolution, error) {
	provider := cfg.ASR.Provider
	preferred := config.PreferredWhisperProviderFor(cfg.Platform())
	sherpaCfg := cfg.ASR
	sherpaCfg.Provider = asr.ProviderCPU
	deps := asr.ResolverDeps{
		PreferredWhisperProvider: preferred,
		NumThreads:               cfg.ASR.NumThreads,
		NewSherpa:                func() asr.Engine { return newASREngine(sherpaCfg) },
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
	if probeGPUHook != nil {
		deps.ProbeAccelerator = func(provider string, device int) (asr.Accelerator, error) {
			name, err := probeGPUHook()
			return asr.Accelerator{Provider: provider, Device: device, Name: name}, err
		}
	}
	if hook := newWhisperEngineHook; hook != nil {
		deps.NewWhisper = func(modelPath, provider string, device, threads int) (asr.Engine, error) {
			return hook(modelPath, device, threads, provider != asr.ProviderCPU)
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
		_ = engine.Close()
		reason := fmt.Sprintf("whisper-cpp load failed: %v", err)
		return loadSherpaFallback(ctx, cfg, reason, stderr)
	}
	if resolution.Engine != asr.EngineWhisper {
		return engine, resolution, nil
	}
	report := asr.BackendReport{}
	if reporter, ok := engine.(asr.BackendReporter); ok {
		report = reporter.ActiveBackend()
	}
	confirmed, action := asr.ConfirmWhisperBackend(cfg.ASR.Engine, cfg.ASR.Provider, resolution, report)
	switch action {
	case asr.BackendFallback:
		reason := cliBackendConfirmationFailure(resolution.Provider, report)
		_ = engine.Close()
		return loadSherpaFallback(ctx, cfg, reason, stderr)
	case asr.BackendUnavailable:
		reason := cliBackendConfirmationFailure(resolution.Provider, report)
		_ = engine.Close()
		return engine, confirmed, apperr.New(apperr.CodeASRBackendUnavailable, "confirm whisper backend", errors.New(reason))
	default:
		if resolution.Provider != asr.ProviderCPU && confirmed.Provider == asr.ProviderCPU {
			fmt.Fprintf(stderr, "whisper-cpp backend downgraded to cpu (requested %s, reported backend %q)\n", resolution.Provider, report.DeviceName)
		}
		resolution = confirmed
	}
	return engine, resolution, nil
}

func loadSherpaFallback(ctx context.Context, cfg config.Config, reason string, stderr io.Writer) (asr.Engine, asr.Resolution, error) {
	fallbackCfg := cfg
	fallbackCfg.ASR.Engine = asr.EngineSherpa
	fallbackCfg.ASR.Provider = asr.ProviderCPU
	fallback, resolution, err := resolveASREngine(fallbackCfg)
	if err != nil {
		return nil, asr.Resolution{}, fmt.Errorf("%s; sherpa fallback resolution failed: %w", reason, err)
	}
	resolution.FallbackReason = reason
	fmt.Fprintf(stderr, "%s\n", reason)
	fmt.Fprintf(stderr, "falling back to sherpa-onnx: %s\n", reason)
	if err := validateResolvedSherpaConfig(fallbackCfg); err != nil {
		return fallback, resolution, err
	}
	if err := validateModelForUseFn(fallbackCfg); err != nil {
		return fallback, resolution, err
	}
	if err := fallback.Load(ctx); err != nil {
		return fallback, resolution, fmt.Errorf("sherpa fallback load failed: %w", err)
	}
	return fallback, resolution, nil
}

func cliBackendConfirmationFailure(provider string, report asr.BackendReport) string {
	reported := report.Provider
	if reported == "" {
		reported = "unconfirmed"
	}
	if report.DeviceName != "" {
		reported += " (" + report.DeviceName + ")"
	}
	return fmt.Sprintf("whisper-cpp requested %s but native diagnostics reported %s", provider, reported)
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

func prepareInjection(ctx context.Context, cfg config.Config, stderr io.Writer) (*preparedInjection, error) {
	w := newInjector(cfg.Injection)
	if err := w.Available(ctx); err != nil {
		return nil, err
	}
	prepared := &preparedInjection{injector: w, stderr: stderr}
	if cfg.Focus.Enabled {
		guard := newFocusGuard(cfg)
		if err := guard.CaptureStart(ctx, 0); err != nil {
			return nil, err
		}
		prepared.guard = guard
	}
	return prepared, nil
}

func (p *preparedInjection) TypeText(ctx context.Context, text string) error {
	request := inject.Request{Text: text}
	if p.guard != nil {
		target, warning, err := p.guard.ResolveForInjection(ctx)
		if err != nil {
			return err
		}
		defer p.guard.Release(target)
		request.Target.Focus = target
		request.ValidateTarget = p.guard.Validate
		if warning != nil && p.stderr != nil {
			fmt.Fprintf(p.stderr, "warning: %s\n", warning.Error())
		}
	}
	if err := p.injector.Inject(ctx, request); err != nil {
		return normalizeCLIError(apperr.CodeInjectionFailed, "inject text", err)
	}
	return nil
}

func (p *preparedInjection) Reset() {
	if p != nil && p.guard != nil {
		p.guard.Reset()
	}
}

type focusGuardAdapter struct {
	guard    *focus.Guard
	provider focus.Provider
}

func (g *focusGuardAdapter) CaptureStart(ctx context.Context, expectedPID int) error {
	return g.guard.CaptureStart(ctx, expectedPID)
}

func (g *focusGuardAdapter) ResolveForInjection(ctx context.Context) (focus.Target, *focus.Change, error) {
	return g.guard.ResolveForInjection(ctx)
}

func (g *focusGuardAdapter) Release(target focus.Target) {
	g.provider.Release(target)
}

func (g *focusGuardAdapter) Validate(ctx context.Context, target focus.Target) error {
	return focus.ValidateTarget(ctx, g.provider, target)
}

func (g *focusGuardAdapter) Reset() {
	g.guard.Reset()
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
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		paths := config.CurrentPlatformPaths()
		opts := modelinstall.InstallOptions{
			Dir:      *dir,
			StateDir: paths.StateDir,
			CacheDir: paths.CacheDir,
			Progress: func(progress modelinstall.Progress) {
				if progress.TotalBytes > 0 {
					fmt.Fprintf(stderr, "%s %s %.0f%%\n", progress.Item, progress.Phase, float64(progress.BytesDownloaded)*100/float64(progress.TotalBytes))
				} else {
					fmt.Fprintf(stderr, "%s %s\n", progress.Item, progress.Phase)
				}
			},
		}
		install := func(locked modelinstall.InstallOptions, kind string) bool {
			var (
				path string
				err  error
			)
			switch kind {
			case model.ParakeetUnifiedFP32ID:
				path, err = installParakeetUnifiedFP32(ctx, locked)
			case "parakeet-v3-int8":
				path, err = installParakeetV3Int8(ctx, locked)
			case "silero-vad":
				path, err = installSileroVAD(ctx, locked)
			default:
				path, err = installWhisper(ctx, kind, locked)
			}
			if err != nil {
				fmt.Fprintf(stderr, "%s: %v\n", kind, err)
				return false
			}
			fmt.Fprintln(stdout, path)
			return true
		}
		ok := true
		err := modelinstall.WithLock(ctx, opts, func(locked modelinstall.InstallOptions) error {
			if name == "all" {
				ok = install(locked, model.ParakeetUnifiedFP32ID) && ok
				ok = install(locked, "silero-vad") && ok
				ok = install(locked, config.Defaults().ASR.WhisperModel) && ok
			} else {
				ok = install(locked, name)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitcode.ForErrorCode(apperr.Code(err))
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
		"rss_peak_bytes": metrics.PeakRSSBytes(),
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
	for _, result := range doctor.Current().Checks(context.Background(), cfg) {
		switch result.Level {
		case doctor.Fail:
			failures++
			fmt.Fprintf(stdout, "FAIL %-18s %v\n", result.Name, result.Err)
		case doctor.Warn:
			fmt.Fprintf(stdout, "WARN %-18s %s\n", result.Name, result.Detail)
		case doctor.Info:
			fmt.Fprintf(stdout, "INFO %-18s %s\n", result.Name, result.Detail)
		default:
			fmt.Fprintf(stdout, "OK   %-18s\n", result.Name)
		}
	}
	if cliPlatform == "darwin" {
		probeCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		response, appErr := controlSend(probeCtx, cfg.Daemon.Socket, control.NewRequest("permissions", nil))
		cancel()
		if appErr != nil {
			fmt.Fprintf(stdout, "INFO %-18s not running (%v)\n", "app host", appErr)
		} else if !response.OK {
			failures++
			fmt.Fprintf(stdout, "FAIL %-18s %s: %s\n", "app host", response.Error.Code, response.Error.Message)
		} else {
			host := ""
			if response.Status.Platform != nil {
				host = response.Status.Platform.Host
			}
			fmt.Fprintf(stdout, "OK   %-18s host=%s\n", "app host", host)
			fmt.Fprintf(stdout, "INFO %-18s microphone=%v accessibility=%v input_monitoring=%v (informational; not required)\n", "permissions", response.Data["microphone"], response.Data["accessibility"], response.Data["input_monitoring"])
		}
	}
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

func exitForErr(err error) int {
	switch apperr.Code(err) {
	case apperr.CodeASRModelMissing, apperr.CodeASRModelInvalid, apperr.CodeASRBackendUnavailable:
		return exitcode.ModelInvalid
	case apperr.CodeAudioBackendUnavailable, apperr.CodeAudioDeviceNotFound, apperr.CodeAudioDeviceDisconnected, apperr.CodeAudioStartFailed:
		return exitcode.PipeWireUnavailable
	case apperr.CodeFocusUnavailable, apperr.CodeFocusChanged, apperr.CodeSecureField:
		return exitcode.SwayUnavailable
	case apperr.CodeInjectorUnavailable, apperr.CodeInjectionFailed:
		return exitcode.WtypeUnavailable
	case apperr.CodePermissionMicrophoneDenied, apperr.CodePermissionAccessibilityDenied, apperr.CodePermissionInputMonitoringDenied:
		return exitcode.Permission
	default:
		return exitcode.Generic
	}
}

func normalizeCLIError(code, operation string, err error) error {
	if err == nil || apperr.Code(err) != apperr.CodeInternalError {
		return err
	}
	return apperr.New(code, operation, err)
}
