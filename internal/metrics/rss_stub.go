//go:build !linux && !darwin

package metrics

func PeakRSSBytes() uint64 { return 0 }
