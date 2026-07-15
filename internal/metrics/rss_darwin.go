//go:build darwin

package metrics

import "syscall"

func PeakRSSBytes() uint64 {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil || usage.Maxrss < 0 {
		return 0
	}
	return uint64(usage.Maxrss)
}
