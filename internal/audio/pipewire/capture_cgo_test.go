//go:build pipewire && cgo && linux

package pipewire

import (
	"context"
	"os"
	"testing"
	"time"

	"waydict/internal/config"
)

func TestPipeWireCaptureLifecycle(t *testing.T) {
	if os.Getenv("WAYDICT_TEST_PIPEWIRE") != "1" {
		t.Skip("set WAYDICT_TEST_PIPEWIRE=1 to run PipeWire integration tests")
	}
	cfg := config.Defaults().Audio
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	buf := make([]float32, 320)
	if _, err := c.Read(ctx, buf); err != nil {
		t.Fatal(err)
	}
	if err := c.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
