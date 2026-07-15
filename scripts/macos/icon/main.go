package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const iconSize = 1024

type point struct{ x, y float64 }

var waveform = []point{
	{214, 512}, {296, 512}, {338, 374}, {408, 660}, {478, 278},
	{548, 746}, {618, 416}, {674, 608}, {716, 512}, {810, 512},
}

func main() {
	output := flag.String("output", "Waydict.icns", "output ICNS")
	flag.Parse()
	types := []struct {
		name string
		size int
	}{
		{"icp4", 16},
		{"icp5", 32},
		{"icp6", 64},
		{"ic07", 128},
		{"ic08", 256},
		{"ic09", 512},
		{"ic10", 1024},
	}
	chunks := make([][]byte, len(types))
	total := 8
	for i, entry := range types {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, render(entry.size)); err != nil {
			panic(err)
		}
		chunks[i] = encoded.Bytes()
		total += 8 + len(chunks[i])
	}
	var icon bytes.Buffer
	icon.WriteString("icns")
	_ = binary.Write(&icon, binary.BigEndian, uint32(total))
	for i, entry := range types {
		icon.WriteString(entry.name)
		_ = binary.Write(&icon, binary.BigEndian, uint32(8+len(chunks[i])))
		icon.Write(chunks[i])
	}
	file, err := os.Create(*output)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	if _, err := file.Write(icon.Bytes()); err != nil {
		panic(err)
	}
}

func render(size int) *image.NRGBA {
	result := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			base := backgroundPixel(x, y, size)
			coverage := waveformCoverage(x, y, size)
			if coverage != 0 {
				base = blendWhite(base, coverage)
			}
			result.SetNRGBA(x, y, base)
		}
	}
	return result
}

func backgroundPixel(x, y, size int) color.NRGBA {
	coverage := sampleCoverage(x, y, size, insideBackground)
	if coverage == 0 {
		return color.NRGBA{}
	}
	designX := float64(x) * iconSize / float64(size)
	designY := float64(y) * iconSize / float64(size)
	t := clamp((designX+designY-216)/1600, 0, 1)
	return color.NRGBA{
		R: uint8(37 + t*(15-37)),
		G: uint8(99 + t*(118-99)),
		B: uint8(235 + t*(110-235)),
		A: uint8(math.Round(255 * coverage)),
	}
}

func insideBackground(x, y float64) bool {
	qx := math.Abs(x-512) - 230
	qy := math.Abs(y-512) - 230
	distance := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - 218
	return distance <= 0
}

func waveformCoverage(x, y, size int) float64 {
	designX := float64(x) * iconSize / float64(size)
	designY := float64(y) * iconSize / float64(size)
	if designX < 180 || designX > 844 || designY < 240 || designY > 784 {
		return 0
	}
	return sampleCoverage(x, y, size, func(px, py float64) bool {
		for i := 1; i < len(waveform); i++ {
			if segmentDistance(point{px, py}, waveform[i-1], waveform[i]) <= 27 {
				return true
			}
		}
		return false
	})
}

func segmentDistance(p, a, b point) float64 {
	dx, dy := b.x-a.x, b.y-a.y
	lengthSquared := dx*dx + dy*dy
	if lengthSquared == 0 {
		return math.Hypot(p.x-a.x, p.y-a.y)
	}
	t := clamp(((p.x-a.x)*dx+(p.y-a.y)*dy)/lengthSquared, 0, 1)
	return math.Hypot(p.x-(a.x+t*dx), p.y-(a.y+t*dy))
}

func sampleCoverage(x, y, size int, inside func(float64, float64) bool) float64 {
	covered := 0
	for _, oy := range []float64{0.25, 0.75} {
		for _, ox := range []float64{0.25, 0.75} {
			px := (float64(x) + ox) * iconSize / float64(size)
			py := (float64(y) + oy) * iconSize / float64(size)
			if inside(px, py) {
				covered++
			}
		}
	}
	return float64(covered) / 4
}

func blendWhite(base color.NRGBA, coverage float64) color.NRGBA {
	return color.NRGBA{
		R: uint8(math.Round(float64(base.R)*(1-coverage) + 255*coverage)),
		G: uint8(math.Round(float64(base.G)*(1-coverage) + 255*coverage)),
		B: uint8(math.Round(float64(base.B)*(1-coverage) + 255*coverage)),
		A: base.A,
	}
}

func clamp(value, low, high float64) float64 {
	return math.Min(math.Max(value, low), high)
}
