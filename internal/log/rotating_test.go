package log

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingWriterProtectsAndRetainsFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "waydict.log")
	writer, err := OpenRotating(path, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"first\n", "second\n", "third\n"} {
		if _, err := writer.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, path + ".1"} {
		info, err := os.Stat(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode = %o", candidate, info.Mode().Perm())
		}
	}
	lines, err := TailLines(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, "\n") != "first\nsecond\nthird" {
		t.Fatalf("tail = %q", lines)
	}
	lines, err = TailLines(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, "\n") != "second\nthird" {
		t.Fatalf("limited tail = %q", lines)
	}
}

func TestRotatingWriterRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "waydict.log")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenRotating(path, 1024, 1); err == nil {
		t.Fatal("expected symlink rejection")
	}
	if _, err := TailLines(path, 100); err == nil {
		t.Fatal("expected tail symlink rejection")
	}
}
