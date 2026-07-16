package log

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	DefaultMaxBytes = 5 << 20
	DefaultBackups  = 3
)

type RotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	backups  int
	file     *os.File
	size     int64
}

func OpenRotating(path string, maxBytes int64, backups int) (*RotatingWriter, error) {
	if path == "" || maxBytes <= 0 || backups < 0 {
		return nil, fmt.Errorf("invalid rotating log configuration")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	w := &RotatingWriter{path: path, maxBytes: maxBytes, backups: backups}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RotatingWriter) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := errors.Join(w.file.Sync(), w.file.Close())
	w.file = nil
	return err
}

func (w *RotatingWriter) open() error {
	if info, err := os.Lstat(w.path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlink log path %s", w.path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	w.file = file
	w.size = info.Size()
	return nil
}

func (w *RotatingWriter) rotate() error {
	if err := errors.Join(w.file.Sync(), w.file.Close()); err != nil {
		return err
	}
	w.file = nil
	if w.backups == 0 {
		if err := os.Remove(w.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		_ = os.Remove(fmt.Sprintf("%s.%d", w.path, w.backups))
		for index := w.backups - 1; index >= 1; index-- {
			from := fmt.Sprintf("%s.%d", w.path, index)
			to := fmt.Sprintf("%s.%d", w.path, index+1)
			if err := os.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if err := os.Rename(w.path, w.path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return w.open()
}

func TailLines(path string, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	lines := make([]string, 0, limit)
	for index := DefaultBackups; index >= 0; index-- {
		candidate := path
		if index > 0 {
			candidate = fmt.Sprintf("%s.%d", path, index)
		}
		if err := scanLogLines(candidate, func(line string) {
			if len(lines) == limit {
				copy(lines, lines[1:])
				lines[len(lines)-1] = line
			} else {
				lines = append(lines, line)
			}
		}); err != nil {
			return nil, err
		}
	}
	return lines, nil
}

func scanLogLines(path string, consume func(string)) error {
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return fmt.Errorf("refusing non-regular log path %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(before, after) {
		return fmt.Errorf("log path changed while opening %s", path)
	}
	scanner := bufio.NewScanner(io.LimitReader(file, DefaultMaxBytes+1))
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 256*1024)
	for scanner.Scan() {
		consume(scanner.Text())
	}
	return scanner.Err()
}
