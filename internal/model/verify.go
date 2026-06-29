package model

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"sway-voice/internal/config"
)

type CheckOptions struct {
	StrictSizes bool
}

type CheckResult struct {
	Dir    string      `json:"dir"`
	OK     bool        `json:"ok"`
	Items  []CheckItem `json:"items"`
	Errors []string    `json:"errors,omitempty"`
}

type CheckItem struct {
	Path    string `json:"path"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	Size    int64  `json:"size,omitempty"`
}

func CheckConfig(cfg config.Config, opts CheckOptions) CheckResult {
	res := CheckDir(cfg.ASR.ModelDir, opts)
	if cfg.ASR.Provider != "cpu" {
		res.addErr("asr.provider must be cpu")
	}
	return res
}

func CheckDir(dir string, opts CheckOptions) CheckResult {
	res := CheckResult{Dir: dir, OK: true}
	for _, req := range RequiredFiles() {
		path := filepath.Join(dir, req.Name)
		st, err := os.Stat(path)
		item := CheckItem{Path: path}
		if err != nil {
			item.OK = false
			item.Message = err.Error()
			res.addErr(fmt.Sprintf("%s: %v", req.Name, err))
		} else if st.IsDir() {
			item.OK = false
			item.Message = "is a directory"
			res.addErr(req.Name + " is a directory")
		} else {
			item.Size = st.Size()
			item.OK = true
			if err := checkReadable(path); err != nil {
				item.OK = false
				item.Message = err.Error()
				res.addErr(fmt.Sprintf("%s is not readable: %v", req.Name, err))
			}
			if opts.StrictSizes && st.Size() < req.MinSize {
				item.OK = false
				item.Message = fmt.Sprintf("size %d is below plausible minimum %d", st.Size(), req.MinSize)
				res.addErr(req.Name + " size is implausibly small")
			}
		}
		res.Items = append(res.Items, item)
	}
	if err := checkTokens(filepath.Join(dir, "tokens.txt")); err != nil {
		res.addErr(err.Error())
	}
	if err := verifyChecksums(dir); err != nil {
		res.addErr(err.Error())
	}
	return res
}

func (r *CheckResult) addErr(msg string) {
	r.OK = false
	r.Errors = append(r.Errors, msg)
}

func checkTokens(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if strings.TrimSpace(string(b)) == "" {
		return fmt.Errorf("tokens.txt is empty")
	}
	return nil
}

func checkReadable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return f.Close()
}

func verifyChecksums(dir string) error {
	path := filepath.Join(dir, DefaultChecksumFile)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return fmt.Errorf("invalid checksum line %q", line)
		}
		want := strings.ToLower(fields[0])
		name := strings.TrimPrefix(fields[1], "*")
		if err := validateChecksumName(name); err != nil {
			return err
		}
		got, err := fileSHA256(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("checksum mismatch for %s", name)
		}
	}
	return scanner.Err()
}

func validateChecksumName(name string) error {
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("unsafe checksum path %q", name)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
