package audio

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

func WriteWAVFloat32(path string, samples []float32, sampleRate int) error {
	if sampleRate <= 0 {
		return fmt.Errorf("sample rate must be positive")
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dataBytes := uint32(len(samples) * 4)
	if err := writeString(f, "RIFF"); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(36)+dataBytes); err != nil {
		return err
	}
	if err := writeString(f, "WAVEfmt "); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(16)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(3)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(1)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(sampleRate)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(sampleRate*4)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(4)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(32)); err != nil {
		return err
	}
	if err := writeString(f, "data"); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, dataBytes); err != nil {
		return err
	}
	for _, sample := range samples {
		if err := binary.Write(f, binary.LittleEndian, math.Float32bits(sample)); err != nil {
			return err
		}
	}
	return nil
}

func writeString(f *os.File, value string) error {
	_, err := f.WriteString(value)
	return err
}
