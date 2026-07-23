//go:build sherpa && cgo

package sherpa

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"waydict/internal/asr"
	"waydict/internal/config"

	onnx "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

type Engine struct {
	cfg        config.ASR
	mu         sync.Mutex
	recognizer *onnx.OfflineRecognizer
	loaded     bool
}

const minDecodeAudio = 100 * time.Millisecond

func New(cfg config.ASR) *Engine {
	return &Engine{cfg: cfg}
}

func (e *Engine) Name() string {
	return "sherpa-onnx"
}

func (e *Engine) Loaded() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loaded
}

func (e *Engine) Load(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loaded {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if e.cfg.Provider != "cpu" && e.cfg.Provider != "cuda" {
		return fmt.Errorf("unsupported ASR provider %q", e.cfg.Provider)
	}
	paths := ModelPaths(e.cfg)
	cfg := &onnx.OfflineRecognizerConfig{
		FeatConfig: onnx.FeatureConfig{
			SampleRate: 16000,
			FeatureDim: 80,
		},
		ModelConfig: onnx.OfflineModelConfig{
			Transducer: onnx.OfflineTransducerModelConfig{
				Encoder: paths.Encoder,
				Decoder: paths.Decoder,
				Joiner:  paths.Joiner,
			},
			Tokens:     paths.Tokens,
			NumThreads: e.cfg.NumThreads,
			Provider:   e.cfg.Provider,
			ModelType:  e.cfg.ModelType,
		},
		DecodingMethod: e.cfg.DecodingMethod,
		MaxActivePaths: e.cfg.MaxActivePaths,
		BlankPenalty:   e.cfg.BlankPenalty,
	}
	rec := onnx.NewOfflineRecognizer(cfg)
	if rec == nil {
		return fmt.Errorf("failed to create sherpa offline recognizer")
	}
	e.recognizer = rec
	e.loaded = true
	return nil
}

func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.recognizer != nil {
		onnx.DeleteOfflineRecognizer(e.recognizer)
	}
	e.recognizer = nil
	e.loaded = false
	return nil
}

func (e *Engine) Transcribe(ctx context.Context, segment asr.AudioSegment) (asr.Transcript, error) {
	if len(segment.Samples) == 0 || tooShortForDecode(segment) {
		return emptyTranscript(segment, 0), nil
	}
	if segment.SampleRate <= 0 {
		return asr.Transcript{}, fmt.Errorf("invalid segment sample rate %d", segment.SampleRate)
	}
	if err := e.Load(ctx); err != nil {
		return asr.Transcript{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.recognizer == nil {
		return asr.Transcript{}, fmt.Errorf("recognizer is not loaded")
	}
	select {
	case <-ctx.Done():
		return asr.Transcript{}, ctx.Err()
	default:
	}
	start := time.Now()
	stream := onnx.NewOfflineStream(e.recognizer)
	if stream == nil {
		return asr.Transcript{}, fmt.Errorf("failed to create sherpa offline stream")
	}
	defer onnx.DeleteOfflineStream(stream)
	stream.AcceptWaveform(segment.SampleRate, segment.Samples)
	e.recognizer.Decode(stream)
	result := stream.GetResult()
	decodeDuration := time.Since(start)
	if result == nil {
		return emptyTranscript(segment, decodeDuration), nil
	}
	timestamps := make([]float64, len(result.Timestamps))
	for i, ts := range result.Timestamps {
		timestamps[i] = float64(ts)
	}
	rtf := 0.0
	if segment.Duration > 0 {
		rtf = decodeDuration.Seconds() / segment.Duration.Seconds()
	}
	text := result.Text
	return asr.Transcript{
		SegmentID:       segment.ID,
		Text:            text,
		Tokens:          append([]string(nil), result.Tokens...),
		TokenTimestamps: timestamps,
		StartedAt:       segment.StartedAt,
		AudioDuration:   segment.Duration,
		DecodeDuration:  decodeDuration,
		RealTimeFactor:  rtf,
		Empty:           strings.TrimSpace(text) == "",
	}, nil
}

func tooShortForDecode(segment asr.AudioSegment) bool {
	sampleRate := segment.SampleRate
	if sampleRate <= 0 {
		sampleRate = 16000
	}
	minSamples := int(minDecodeAudio.Seconds() * float64(sampleRate))
	if minSamples < 1 {
		minSamples = 1
	}
	return len(segment.Samples) < minSamples
}

func emptyTranscript(segment asr.AudioSegment, decodeDuration time.Duration) asr.Transcript {
	return asr.Transcript{
		SegmentID:      segment.ID,
		StartedAt:      segment.StartedAt,
		AudioDuration:  segment.Duration,
		DecodeDuration: decodeDuration,
		Empty:          true,
	}
}
