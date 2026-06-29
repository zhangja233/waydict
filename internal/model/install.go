package model

import (
	"archive/tar"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type InstallOptions struct {
	Dir string
	URL string
}

func InstallParakeetV3Int8(ctx context.Context, opts InstallOptions) (string, error) {
	base := opts.Dir
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share", "sway-voice", "models")
	}
	url := opts.URL
	if url == "" {
		url = ParakeetV3ArchiveURL
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(base, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	archivePath := filepath.Join(tmp, filepath.Base(url))
	if err := download(ctx, url, archivePath); err != nil {
		return "", err
	}
	if err := unpackTarBz2(archivePath, tmp); err != nil {
		return "", err
	}
	extracted := filepath.Join(tmp, SherpaParakeetV3Int8)
	if _, err := os.Stat(extracted); err != nil {
		return "", err
	}
	final := filepath.Join(base, ParakeetV3Int8ID)
	staging := final + ".new"
	_ = os.RemoveAll(staging)
	if err := os.Rename(extracted, staging); err != nil {
		return "", err
	}
	if err := writeChecksums(staging); err != nil {
		return "", err
	}
	backup := final + ".old"
	_ = os.RemoveAll(backup)
	if _, err := os.Stat(final); err == nil {
		if err := os.Rename(final, backup); err != nil {
			return "", err
		}
	}
	if err := os.Rename(staging, final); err != nil {
		_ = os.Rename(backup, final)
		return "", err
	}
	_ = os.RemoveAll(backup)
	link := filepath.Join(base, "current")
	tmpLink := filepath.Join(base, ".current.new")
	_ = os.Remove(tmpLink)
	if err := os.Symlink(final, tmpLink); err != nil {
		return "", err
	}
	if err := os.Rename(tmpLink, link); err != nil {
		_ = os.Remove(tmpLink)
		return "", err
	}
	return final, nil
}

func download(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func unpackTarBz2(path, dst string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return unpackTar(tar.NewReader(bzip2.NewReader(f)), dst)
}

func unpackTar(tr *tar.Reader, dst string) error {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dst, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, modeOrDefault(hdr.FileInfo().Mode(), 0755)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, modeOrDefault(hdr.FileInfo().Mode(), 0644))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}

func safeJoin(base, name string) (string, error) {
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean == ".." {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return filepath.Join(base, clean), nil
}

func modeOrDefault(mode os.FileMode, def os.FileMode) os.FileMode {
	if mode == 0 {
		return def
	}
	return mode
}

func writeChecksums(dir string) error {
	out, err := os.Create(filepath.Join(dir, DefaultChecksumFile))
	if err != nil {
		return err
	}
	defer out.Close()
	for _, req := range RequiredFiles() {
		path := filepath.Join(dir, req.Name)
		sum, err := fileSHA256(path)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "%s  %s\n", sum, req.Name); err != nil {
			return err
		}
	}
	return nil
}

func BytesSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
