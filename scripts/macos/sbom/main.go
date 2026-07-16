package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type module struct {
	Path    string
	Version string
	Main    bool
}

type externalRef struct {
	Category string `json:"referenceCategory"`
	Type     string `json:"referenceType"`
	Locator  string `json:"referenceLocator"`
}

type pkg struct {
	Name             string        `json:"name"`
	SPDXID           string        `json:"SPDXID"`
	VersionInfo      string        `json:"versionInfo,omitempty"`
	DownloadLocation string        `json:"downloadLocation"`
	FilesAnalyzed    bool          `json:"filesAnalyzed"`
	LicenseConcluded string        `json:"licenseConcluded"`
	LicenseDeclared  string        `json:"licenseDeclared"`
	CopyrightText    string        `json:"copyrightText"`
	ExternalRefs     []externalRef `json:"externalRefs,omitempty"`
}

type relationship struct {
	Element string `json:"spdxElementId"`
	Type    string `json:"relationshipType"`
	Related string `json:"relatedSpdxElement"`
}

type document struct {
	SPDXVersion       string         `json:"spdxVersion"`
	DataLicense       string         `json:"dataLicense"`
	SPDXID            string         `json:"SPDXID"`
	Name              string         `json:"name"`
	DocumentNamespace string         `json:"documentNamespace"`
	CreationInfo      creationInfo   `json:"creationInfo"`
	Packages          []pkg          `json:"packages"`
	Relationships     []relationship `json:"relationships"`
}

type creationInfo struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

func main() {
	version := flag.String("version", "", "release version")
	commit := flag.String("commit", "", "source commit")
	output := flag.String("output", "", "output path")
	flag.Parse()
	if *version == "" || *commit == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "version, commit, and output are required")
		os.Exit(2)
	}
	modules, err := listModules()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	created := sourceTime()
	packages := []pkg{{Name: "waydict", SPDXID: "SPDXRef-Package-waydict", VersionInfo: *version, DownloadLocation: "https://github.com/zhangja233/waydict", FilesAnalyzed: false, LicenseConcluded: "MIT", LicenseDeclared: "MIT", CopyrightText: "NOASSERTION"}}
	relationships := []relationship{{Element: "SPDXRef-DOCUMENT", Type: "DESCRIBES", Related: "SPDXRef-Package-waydict"}}
	for _, mod := range modules {
		if mod.Main {
			continue
		}
		id := spdxID(mod.Path)
		license := knownLicense(mod.Path)
		packages = append(packages, pkg{Name: mod.Path, SPDXID: id, VersionInfo: mod.Version, DownloadLocation: "https://" + mod.Path, FilesAnalyzed: false, LicenseConcluded: license, LicenseDeclared: license, CopyrightText: "NOASSERTION", ExternalRefs: []externalRef{{Category: "PACKAGE-MANAGER", Type: "purl", Locator: "pkg:golang/" + mod.Path + "@" + mod.Version}}})
		relationships = append(relationships, relationship{Element: "SPDXRef-Package-waydict", Type: "DEPENDS_ON", Related: id})
	}
	for _, native := range []struct{ name, version, license, location string }{
		{"whisper.cpp", "f049fff95a08", "MIT", "https://github.com/ggml-org/whisper.cpp"},
		{"sherpa-onnx", "1.13.3", "Apache-2.0", "https://github.com/k2-fsa/sherpa-onnx"},
		{"onnxruntime", "1.24.4", "MIT", "https://github.com/microsoft/onnxruntime"},
	} {
		id := spdxID(native.name)
		packages = append(packages, pkg{Name: native.name, SPDXID: id, VersionInfo: native.version, DownloadLocation: native.location, FilesAnalyzed: false, LicenseConcluded: native.license, LicenseDeclared: native.license, CopyrightText: "NOASSERTION"})
		relationships = append(relationships, relationship{Element: "SPDXRef-Package-waydict", Type: "DEPENDS_ON", Related: id})
	}
	sort.Slice(packages[1:], func(i, j int) bool { return packages[i+1].Name < packages[j+1].Name })
	sort.Slice(relationships[1:], func(i, j int) bool { return relationships[i+1].Related < relationships[j+1].Related })
	doc := document{SPDXVersion: "SPDX-2.3", DataLicense: "CC0-1.0", SPDXID: "SPDXRef-DOCUMENT", Name: "Waydict-" + *version, DocumentNamespace: "https://github.com/zhangja233/waydict/releases/download/v" + *version + "/waydict-" + *commit + ".spdx.json", CreationInfo: creationInfo{Created: created, Creators: []string{"Tool: waydict-sbom"}}, Packages: packages, Relationships: relationships}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		panic(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(*output, data, 0o644); err != nil {
		panic(err)
	}
}

func listModules() ([]module, error) {
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list modules: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	var modules []module
	for dec.More() {
		var mod module
		if err := dec.Decode(&mod); err != nil {
			return nil, err
		}
		modules = append(modules, mod)
	}
	return modules, nil
}

func sourceTime() string {
	data, err := exec.Command("git", "show", "-s", "--format=%cI", "HEAD").Output()
	if err == nil {
		if parsed, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(string(data))); parseErr == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return "1970-01-01T00:00:00Z"
}

func knownLicense(path string) string {
	switch path {
	case "github.com/rivo/uniseg":
		return "MIT"
	case "github.com/k2-fsa/sherpa-onnx-go", "github.com/k2-fsa/sherpa-onnx-go-macos":
		return "Apache-2.0"
	default:
		return "NOASSERTION"
	}
}

func spdxID(value string) string {
	var out strings.Builder
	out.WriteString("SPDXRef-Package-")
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			out.WriteRune(r)
		} else {
			out.WriteByte('-')
		}
	}
	return out.String()
}
