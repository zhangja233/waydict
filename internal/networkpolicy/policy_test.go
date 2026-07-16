package networkpolicy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestOutboundNetworkImportsMatchAllowlist(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate policy test")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "build", "result", "third_party":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		relative, _ := filepath.Rel(root, filepath.Dir(path))
		relative = filepath.ToSlash(relative)
		for _, spec := range parsed.Decls {
			declaration, ok := spec.(*ast.GenDecl)
			if !ok || declaration.Tok != token.IMPORT {
				continue
			}
			for _, item := range declaration.Specs {
				importSpec := item.(*ast.ImportSpec)
				name, _ := strconv.Unquote(importSpec.Path.Value)
				allowed, tracked := AllowedNetworkPackages[name]
				if !tracked {
					continue
				}
				matched := false
				for _, prefix := range allowed {
					matched = matched || relative == prefix || strings.HasPrefix(relative, prefix+"/")
				}
				if !matched {
					t.Errorf("%s imports %s outside the outbound network allowlist", filepath.ToSlash(path), name)
				}
			}
		}
		if relative == "internal/control" || relative == "internal/swayipc" {
			assertUnixSocketCalls(t, path, parsed)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertUnixSocketCalls(t *testing.T, path string, parsed *ast.File) {
	t.Helper()
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		networkArgument := -1
		if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == "net" {
			switch selector.Sel.Name {
			case "Listen", "Dial", "DialTimeout":
				networkArgument = 0
			}
		} else if selector.Sel.Name == "DialContext" {
			networkArgument = 1
		}
		if networkArgument < 0 || len(call.Args) <= networkArgument {
			return true
		}
		literal, ok := call.Args[networkArgument].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			t.Errorf("%s uses a dynamic IPC network", filepath.ToSlash(path))
			return true
		}
		network, err := strconv.Unquote(literal.Value)
		if err != nil || network != "unix" {
			t.Errorf("%s uses non-Unix IPC network %q", filepath.ToSlash(path), network)
		}
		return true
	})
}
