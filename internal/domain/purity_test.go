package domain_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCorePackagesArePure enforces the Phase 2 rule: the core logic packages
// import no database, network, filesystem or process packages and never read
// the system clock directly (time is injected).
func TestCorePackagesArePure(t *testing.T) {
	dirs := []string{"../domain", "../stats", "../calibration", "../quality", "../rules", "../ingest/protocol"}
	forbiddenImports := []string{"database/sql", "net", "os", "io/ioutil", "os/exec", "syscall"}
	forbiddenCalls := []string{"time.Now(", "time.Since(", "time.Until(", "time.After(", "time.Tick(", "time.Sleep("}

	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: no sources found (%v)", dir, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f) //nolint:gosec // paths come from a fixed list of repo directories
			if err != nil {
				t.Fatal(err)
			}
			file, err := parser.ParseFile(token.NewFileSet(), f, src, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				first, _, _ := strings.Cut(path, "/")
				if strings.Contains(first, ".") && !strings.HasPrefix(path, "github.com/sellebeww/spillway/") {
					t.Errorf("%s imports third-party package %s", f, path)
				}
				for _, bad := range forbiddenImports {
					if path == bad || strings.HasPrefix(path, bad+"/") {
						t.Errorf("%s imports forbidden package %s", f, path)
					}
				}
			}
			for _, call := range forbiddenCalls {
				if strings.Contains(string(src), call) {
					t.Errorf("%s uses %s (inject the clock instead)", f, call)
				}
			}
		}
	}
}
