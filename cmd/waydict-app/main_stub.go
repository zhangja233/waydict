//go:build !darwin || !cgo

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "waydict-app is supported only on macOS with cgo enabled")
	os.Exit(1)
}
