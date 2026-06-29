package audio

import (
	"math"
	"sync"
)

type Ring struct {
	mu       sync.Mutex
	buf      []float32
	head     int
	size     int
	overruns uint64
}

func NewRing(capacityFrames int) *Ring {
	if capacityFrames < 1 {
		capacityFrames = 1
	}
	return &Ring{buf: make([]float32, capacityFrames)}
}

func (r *Ring) Capacity() int {
	return len(r.buf)
}

func (r *Ring) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.size
}

func (r *Ring) Overruns() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.overruns
}

func (r *Ring) Write(samples []float32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range samples {
		if r.size == len(r.buf) {
			r.head = (r.head + 1) % len(r.buf)
			r.overruns++
			r.size--
		}
		tail := (r.head + r.size) % len(r.buf)
		r.buf[tail] = s
		r.size++
	}
}

func (r *Ring) Read(dst []float32) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := min(len(dst), r.size)
	for i := 0; i < n; i++ {
		dst[i] = r.buf[(r.head+i)%len(r.buf)]
	}
	r.head = (r.head + n) % len(r.buf)
	r.size -= n
	return n
}

func (r *Ring) Snapshot() []float32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]float32, r.size)
	for i := range out {
		out[i] = r.buf[(r.head+i)%len(r.buf)]
	}
	return out
}

func (r *Ring) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.head = 0
	r.size = 0
}

func LevelDBFS(samples []float32) float64 {
	if len(samples) == 0 {
		return -120
	}
	var sum float64
	for _, s := range samples {
		v := float64(s)
		sum += v * v
	}
	rms := math.Sqrt(sum / float64(len(samples)))
	if rms <= 0 {
		return -120
	}
	db := 20 * math.Log10(rms)
	if db < -120 {
		return -120
	}
	return db
}
