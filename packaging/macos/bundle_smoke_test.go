//go:build darwin && cgo

package macos_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDevelopmentBundle(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate bundle smoke test")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	command := exec.Command("make", "build-macos-dev", "VERSION=0.1.0", "BUILD_NUMBER=1")
	command.Dir = root
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatalf("build development bundle: %v\n%s", err, output.String())
	}

	app := filepath.Join(root, "build", "Waydict.app")
	required := []string{
		"Contents/Info.plist",
		"Contents/PkgInfo",
		"Contents/Frameworks",
		"Contents/Frameworks/libonnxruntime.1.24.4.dylib",
		"Contents/Frameworks/libsherpa-onnx-c-api.dylib",
		"Contents/MacOS/waydict-app",
		"Contents/MacOS/waydict",
		"Contents/Resources/Waydict.icns",
		"Contents/Resources/LICENSE",
		"Contents/Resources/README.txt",
		"Contents/Resources/THIRD_PARTY_NOTICES.md",
		"Contents/Resources/model-catalog.json",
		"Contents/Resources/en.lproj/Localizable.strings",
		"Contents/Resources/en.lproj/InfoPlist.strings",
	}
	for _, relative := range required {
		if _, err := os.Stat(filepath.Join(app, relative)); err != nil {
			t.Errorf("bundle path %s: %v", relative, err)
		}
	}
	for _, relative := range []string{"Contents/MacOS/waydict-app", "Contents/MacOS/waydict"} {
		info, err := os.Stat(filepath.Join(app, relative))
		if err == nil && info.Mode()&0111 == 0 {
			t.Errorf("%s is not executable", relative)
		}
		binary := filepath.Join(app, relative)
		assertNoNonSystemAbsoluteDependencies(t, binary)
		data, err := exec.Command("otool", "-l", binary).CombinedOutput()
		if err != nil || !bytes.Contains(data, []byte("@executable_path/../Frameworks")) {
			t.Errorf("%s missing bundle Frameworks rpath: %v\n%s", relative, err, data)
		}
	}
	for _, relative := range []string{"Contents/Frameworks/libonnxruntime.1.24.4.dylib", "Contents/Frameworks/libsherpa-onnx-c-api.dylib"} {
		path := filepath.Join(app, relative)
		assertNoNonSystemAbsoluteDependencies(t, path)
		if data, err := exec.Command("codesign", "--verify", "--strict", path).CombinedOutput(); err != nil {
			t.Errorf("verify %s signature: %v\n%s", relative, err, data)
		}
	}

	plist := filepath.Join(app, "Contents", "Info.plist")
	values := map[string]string{
		"CFBundleIdentifier":            "io.github.zhangja233.waydict",
		"CFBundleName":                  "Waydict",
		"CFBundleDisplayName":           "Waydict",
		"CFBundleExecutable":            "waydict-app",
		"CFBundlePackageType":           "APPL",
		"CFBundleShortVersionString":    "0.1.0",
		"CFBundleVersion":               "1",
		"CFBundleDevelopmentRegion":     "en",
		"CFBundleIconFile":              "Waydict",
		"LSMinimumSystemVersion":        "13.0",
		"LSUIElement":                   "true",
		"LSMultipleInstancesProhibited": "true",
		"NSHighResolutionCapable":       "true",
		"NSMicrophoneUsageDescription":  "Waydict uses the microphone only while you dictate and processes speech locally on this Mac.",
	}
	for key, want := range values {
		got := plistValue(t, plist, key)
		if got != want {
			t.Errorf("Info.plist %s = %q, want %q", key, got, want)
		}
	}
	localizedUsage, err := os.ReadFile(filepath.Join(app, "Contents", "Resources", "en.lproj", "InfoPlist.strings"))
	if err != nil {
		t.Fatal(err)
	}
	wantUsage := `"NSMicrophoneUsageDescription" = "Waydict uses the microphone only while you dictate and processes speech locally on this Mac.";`
	if strings.TrimSpace(string(localizedUsage)) != wantUsage {
		t.Errorf("unexpected InfoPlist.strings: %q", localizedUsage)
	}
	verify := exec.Command("codesign", "--verify", "--strict", app)
	if data, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("verify bundle signature: %v\n%s", err, data)
	}
	entitlements := exec.Command("codesign", "-d", "--entitlements", ":-", app)
	data, err := entitlements.CombinedOutput()
	if err != nil {
		t.Fatalf("read bundle entitlements: %v\n%s", err, data)
	}
	if !bytes.Contains(data, []byte("com.apple.security.device.audio-input")) {
		t.Error("audio-input entitlement is missing")
	}
	if bytes.Contains(data, []byte("com.apple.security.app-sandbox")) {
		t.Error("App Sandbox entitlement must not be present")
	}
}

func assertNoNonSystemAbsoluteDependencies(t *testing.T, path string) {
	t.Helper()
	data, err := exec.Command("otool", "-L", path).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect %s dependencies: %v\n%s", path, err, data)
	}
	for i, line := range strings.Split(string(data), "\n") {
		if i == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || !filepath.IsAbs(fields[0]) {
			continue
		}
		if !strings.HasPrefix(fields[0], "/System/Library/") && !strings.HasPrefix(fields[0], "/usr/lib/") {
			t.Errorf("%s retains absolute non-system dependency %s", path, fields[0])
		}
	}
}

func plistValue(t *testing.T, plist, key string) string {
	t.Helper()
	command := exec.Command("plutil", "-extract", key, "raw", "-o", "-", plist)
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("read Info.plist %s: %v\n%s", key, err, data)
	}
	return strings.TrimSpace(string(data))
}
