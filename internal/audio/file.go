package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mewkiz/flac"
)

type FileAudio struct {
	Samples    []float32
	SampleRate int
	Duration   time.Duration
}

func ReadFile(path string) (FileAudio, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".wav", ".wave":
		return readWAV(path)
	case ".flac":
		return readFLAC(path)
	default:
		return FileAudio{}, fmt.Errorf("unsupported audio file type %q", filepath.Ext(path))
	}
}

func readWAV(path string) (FileAudio, error) {
	f, err := os.Open(path)
	if err != nil {
		return FileAudio{}, err
	}
	defer f.Close()
	var riff [12]byte
	if _, err := io.ReadFull(f, riff[:]); err != nil {
		return FileAudio{}, err
	}
	if string(riff[:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return FileAudio{}, fmt.Errorf("not a RIFF/WAVE file")
	}
	var fmtChunk *wavFormat
	var data []byte
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return FileAudio{}, err
		}
		id := string(hdr[:4])
		size := binary.LittleEndian.Uint32(hdr[4:8])
		chunk := make([]byte, size)
		if _, err := io.ReadFull(f, chunk); err != nil {
			return FileAudio{}, err
		}
		if size%2 == 1 {
			if _, err := f.Seek(1, io.SeekCurrent); err != nil {
				return FileAudio{}, err
			}
		}
		switch id {
		case "fmt ":
			parsed, err := parseWAVFormat(chunk)
			if err != nil {
				return FileAudio{}, err
			}
			fmtChunk = &parsed
		case "data":
			data = chunk
		}
	}
	if fmtChunk == nil {
		return FileAudio{}, fmt.Errorf("missing fmt chunk")
	}
	if len(data) == 0 {
		return FileAudio{}, fmt.Errorf("missing data chunk")
	}
	samples, err := decodeWAVData(*fmtChunk, data)
	if err != nil {
		return FileAudio{}, err
	}
	return FileAudio{
		Samples:    samples,
		SampleRate: int(fmtChunk.sampleRate),
		Duration:   time.Duration(len(samples)) * time.Second / time.Duration(fmtChunk.sampleRate),
	}, nil
}

type wavFormat struct {
	audioFormat   uint16
	channels      uint16
	sampleRate    uint32
	bitsPerSample uint16
}

func parseWAVFormat(chunk []byte) (wavFormat, error) {
	if len(chunk) < 16 {
		return wavFormat{}, fmt.Errorf("fmt chunk too small")
	}
	f := wavFormat{
		audioFormat:   binary.LittleEndian.Uint16(chunk[0:2]),
		channels:      binary.LittleEndian.Uint16(chunk[2:4]),
		sampleRate:    binary.LittleEndian.Uint32(chunk[4:8]),
		bitsPerSample: binary.LittleEndian.Uint16(chunk[14:16]),
	}
	if f.channels == 0 || f.sampleRate == 0 {
		return wavFormat{}, fmt.Errorf("invalid WAV format")
	}
	if f.audioFormat != 1 && f.audioFormat != 3 {
		return wavFormat{}, fmt.Errorf("unsupported WAV audio format %d", f.audioFormat)
	}
	return f, nil
}

func decodeWAVData(f wavFormat, data []byte) ([]float32, error) {
	bytesPerSample := int(f.bitsPerSample / 8)
	if bytesPerSample == 0 || int(f.channels)*bytesPerSample == 0 {
		return nil, fmt.Errorf("invalid bits per sample %d", f.bitsPerSample)
	}
	frameSize := int(f.channels) * bytesPerSample
	frames := len(data) / frameSize
	out := make([]float32, frames)
	for frame := 0; frame < frames; frame++ {
		var sum float64
		for ch := 0; ch < int(f.channels); ch++ {
			off := frame*frameSize + ch*bytesPerSample
			v, err := decodeSample(f.audioFormat, f.bitsPerSample, data[off:off+bytesPerSample])
			if err != nil {
				return nil, err
			}
			sum += float64(v)
		}
		out[frame] = float32(sum / float64(f.channels))
	}
	return out, nil
}

func decodeSample(format, bits uint16, b []byte) (float32, error) {
	switch format {
	case 1:
		switch bits {
		case 8:
			return (float32(b[0]) - 128) / 128, nil
		case 16:
			return float32(int16(binary.LittleEndian.Uint16(b))) / 32768, nil
		case 24:
			v := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
			if v&0x800000 != 0 {
				v |= ^0xffffff
			}
			return float32(v) / 8388608, nil
		case 32:
			return float32(int32(binary.LittleEndian.Uint32(b))) / 2147483648, nil
		default:
			return 0, fmt.Errorf("unsupported PCM bit depth %d", bits)
		}
	case 3:
		if bits != 32 {
			return 0, fmt.Errorf("unsupported IEEE float bit depth %d", bits)
		}
		return math.Float32frombits(binary.LittleEndian.Uint32(b)), nil
	default:
		return 0, fmt.Errorf("unsupported WAV format %d", format)
	}
}

func readFLAC(path string) (FileAudio, error) {
	stream, err := flac.ParseFile(path)
	if err != nil {
		return FileAudio{}, err
	}
	defer stream.Close()
	if stream.Info == nil || stream.Info.SampleRate == 0 || stream.Info.NChannels == 0 || stream.Info.BitsPerSample == 0 {
		return FileAudio{}, fmt.Errorf("invalid FLAC stream info")
	}
	channels := int(stream.Info.NChannels)
	scale := math.Pow(2, float64(stream.Info.BitsPerSample-1))
	var samples []float32
	for {
		frame, err := stream.ParseNext()
		if err == io.EOF {
			break
		}
		if err != nil {
			return FileAudio{}, err
		}
		if len(frame.Subframes) != channels || len(frame.Subframes) == 0 {
			return FileAudio{}, fmt.Errorf("FLAC channel count mismatch")
		}
		n := len(frame.Subframes[0].Samples)
		for i := 0; i < n; i++ {
			var sum float64
			for ch := 0; ch < channels; ch++ {
				sum += float64(frame.Subframes[ch].Samples[i]) / scale
			}
			samples = append(samples, float32(sum/float64(channels)))
		}
	}
	return FileAudio{
		Samples:    samples,
		SampleRate: int(stream.Info.SampleRate),
		Duration:   time.Duration(len(samples)) * time.Second / time.Duration(stream.Info.SampleRate),
	}, nil
}
