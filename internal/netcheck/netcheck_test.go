package netcheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/jamalxcode/laserdot-go"

// Every package the module builds (tests included) must stay clear of the
// banned list.
func TestNoNetworkPackages(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-test", module+"/...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		// vendor/golang.org/x/net/... is the standard library's own copy, used
		// by package net itself.
		if strings.HasPrefix(pkg, "vendor/") {
			continue
		}
		for _, b := range Banned {
			if pkg == b || strings.HasPrefix(pkg, b+"/") {
				t.Errorf("banned network package in the dependency graph: %s", pkg)
			}
		}
	}
}

// exempt is the privacy gate's own test: it opens a TCP listener for the
// sandboxed child to (fail to) reach.
var exempt = filepath.Join("internal", "sandbox", "deny_test.go")

// rawSocketPkgs may only create sockets inside internal/sandbox (its
// IsEnforced probe).
var rawSocketPkgs = map[string]bool{"unix": true, "syscall": true}

// Our code dials and listens on Unix sockets only.
func TestOnlyUnixSockets(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, exempt) {
			return nil
		}
		inSandbox := strings.Contains(path, filepath.Join("internal", "sandbox")+string(filepath.Separator))
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && rawSocketPkgs[pkg.Name] && (sel.Sel.Name == "Socket" || sel.Sel.Name == "Connect") && !inSandbox {
				t.Errorf("%s: %s.%s — raw sockets are only allowed in internal/sandbox", path, pkg.Name, sel.Sel.Name)
			}
			if !ok || pkg.Name != "net" {
				return true
			}
			switch sel.Sel.Name {
			case "Dial", "DialTimeout", "Listen", "ListenPacket":
				lit, ok := call.Args[0].(*ast.BasicLit)
				network, _ := strconv.Unquote(litValue(lit, ok))
				if network != "unix" {
					t.Errorf("%s: net.%s(%s) — only \"unix\" sockets are allowed", path, sel.Sel.Name, litValue(lit, ok))
				}
			case "DialUnix", "ListenUnix", "ResolveUnixAddr":
			default:
				if strings.HasPrefix(sel.Sel.Name, "Dial") || strings.HasPrefix(sel.Sel.Name, "Listen") || strings.HasPrefix(sel.Sel.Name, "Lookup") {
					t.Errorf("%s: net.%s is not allowed (no network, spec §7.1)", path, sel.Sel.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func litValue(lit *ast.BasicLit, ok bool) string {
	if !ok || lit == nil {
		return "<not a literal>"
	}
	return lit.Value
}
