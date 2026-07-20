package asr

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// RemoteCommand is the control command a client sends to decode a segment on
// another host's engine.
const RemoteCommand = "transcribe"

// CodecPCMS16LE is the only wire codec the remote engine speaks today. The
// remote protocol still names it explicitly so a compressed codec can be added
// without a protocol break: an old peer rejects the unknown name and the client
// falls back locally instead of decoding garbage.
const CodecPCMS16LE = "pcm_s16le"

// EncodePCMS16LE converts normalized samples to little-endian int16, halving
// what goes on the wire versus float32. The 32768 scale matches the decode side
// in internal/audio.
func EncodePCMS16LE(samples []float32) []byte {
	out := make([]byte, len(samples)*2)
	for i, sample := range samples {
		scaled := float64(sample) * 32768
		// Clamp rather than wrap: a sample past full scale is a clipped mic,
		// and wrapping would turn it into loud noise the model has to decode.
		if scaled > math.MaxInt16 {
			scaled = math.MaxInt16
		} else if scaled < math.MinInt16 {
			scaled = math.MinInt16
		}
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(scaled)))
	}
	return out
}

// DecodePCMS16LE is the inverse of EncodePCMS16LE.
func DecodePCMS16LE(payload []byte) ([]float32, error) {
	if len(payload)%2 != 0 {
		return nil, fmt.Errorf("pcm_s16le payload of %d bytes is not a whole number of samples", len(payload))
	}
	samples := make([]float32, len(payload)/2)
	for i := range samples {
		samples[i] = float32(int16(binary.LittleEndian.Uint16(payload[i*2:]))) / 32768
	}
	return samples, nil
}

// EncodeSegmentArgs describes a segment for the peer. The samples themselves
// travel as the request's binary payload, not in here.
func EncodeSegmentArgs(segment AudioSegment, codec string) map[string]any {
	return map[string]any{
		"segment_id":      segment.ID,
		"sample_rate":     segment.SampleRate,
		"codec":           codec,
		"duration_ms":     segment.Duration.Milliseconds(),
		"degraded":        segment.Degraded,
		"capture_overrun": segment.CaptureOverrun,
	}
}

// DecodeSegment rebuilds a segment from a peer's args and payload. StartedAt is
// left zero: only the caller's own clock is meaningful, and the field is unused
// by every engine.
func DecodeSegment(args map[string]any, payload []byte) (AudioSegment, error) {
	var wire struct {
		SegmentID      string `json:"segment_id"`
		SampleRate     int    `json:"sample_rate"`
		Codec          string `json:"codec"`
		DurationMS     int64  `json:"duration_ms"`
		Degraded       bool   `json:"degraded"`
		CaptureOverrun bool   `json:"capture_overrun"`
	}
	if err := remarshal(args, &wire); err != nil {
		return AudioSegment{}, fmt.Errorf("malformed segment args: %w", err)
	}
	if wire.Codec != CodecPCMS16LE {
		return AudioSegment{}, fmt.Errorf("unsupported codec %q", wire.Codec)
	}
	if wire.SampleRate <= 0 {
		return AudioSegment{}, fmt.Errorf("invalid sample rate %d", wire.SampleRate)
	}
	samples, err := DecodePCMS16LE(payload)
	if err != nil {
		return AudioSegment{}, err
	}
	return AudioSegment{
		ID:             wire.SegmentID,
		Samples:        samples,
		SampleRate:     wire.SampleRate,
		Duration:       time.Duration(wire.DurationMS) * time.Millisecond,
		Degraded:       wire.Degraded,
		CaptureOverrun: wire.CaptureOverrun,
	}, nil
}

// EncodeTranscript renders a decode result for the control response body.
func EncodeTranscript(transcript Transcript) map[string]any {
	return map[string]any{
		"segment_id":        transcript.SegmentID,
		"text":              transcript.Text,
		"tokens":            transcript.Tokens,
		"token_timestamps":  transcript.TokenTimestamps,
		"audio_duration_ms": transcript.AudioDuration.Milliseconds(),
		"decode_ms":         transcript.DecodeDuration.Milliseconds(),
		"rtf":               transcript.RealTimeFactor,
		"empty":             transcript.Empty,
	}
}

// DecodeTranscript is the inverse of EncodeTranscript. The local segment
// supplies StartedAt, which never crossed the wire.
func DecodeTranscript(data map[string]any, segment AudioSegment) (Transcript, error) {
	var wire struct {
		SegmentID       string    `json:"segment_id"`
		Text            string    `json:"text"`
		Tokens          []string  `json:"tokens"`
		TokenTimestamps []float64 `json:"token_timestamps"`
		AudioDurationMS int64     `json:"audio_duration_ms"`
		DecodeMS        int64     `json:"decode_ms"`
		RTF             float64   `json:"rtf"`
		Empty           bool      `json:"empty"`
	}
	if err := remarshal(data, &wire); err != nil {
		return Transcript{}, fmt.Errorf("malformed transcript response: %w", err)
	}
	return Transcript{
		SegmentID:       wire.SegmentID,
		Text:            wire.Text,
		Tokens:          wire.Tokens,
		TokenTimestamps: wire.TokenTimestamps,
		StartedAt:       segment.StartedAt,
		AudioDuration:   time.Duration(wire.AudioDurationMS) * time.Millisecond,
		DecodeDuration:  time.Duration(wire.DecodeMS) * time.Millisecond,
		RealTimeFactor:  wire.RTF,
		Empty:           wire.Empty,
	}, nil
}

// remarshal moves a decoded JSON map into a typed struct, which beats a pile of
// type switches over map[string]any.
func remarshal(from any, into any) error {
	encoded, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, into)
}
