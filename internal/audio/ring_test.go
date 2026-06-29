package audio

import "testing"

func TestRingOverrun(t *testing.T) {
	r := NewRing(3)
	r.Write([]float32{1, 2, 3, 4})
	if r.Overruns() != 1 {
		t.Fatalf("overruns = %d", r.Overruns())
	}
	out := make([]float32, 3)
	n := r.Read(out)
	if n != 3 {
		t.Fatalf("read %d", n)
	}
	want := []float32{2, 3, 4}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("out[%d] = %v, want %v", i, out[i], want[i])
		}
	}
}
