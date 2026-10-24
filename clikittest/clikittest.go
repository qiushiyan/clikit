// Package clikittest runs a clikit App in tests exactly as main does, and pins
// every help page against golden files.
package clikittest

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/qiushiyan/clikit"
)

// Result is what one invocation produced.
type Result struct {
	Code           int
	Stdout, Stderr string
}

// Run invokes app with args, capturing stdout and stderr. Commands may write
// from several goroutines. Set app.Stdin to feed input.
func Run(app clikit.App, args ...string) Result {
	var stdout, stderr lockedBuffer
	app.Stdout, app.Stderr = &stdout, &stderr
	code := app.Run(args)
	return Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// helpPages renders the help of the root and of every visible command, keyed
// by command path ("" for the root, "repo add" for a nested command). Each page
// must exit 0 with nothing on stderr.
func helpPages(t testing.TB, app clikit.App) map[string]string {
	t.Helper()
	if r := Run(app, "--help"); r.Code != 0 || r.Stderr != "" {
		t.Fatalf("--help: exit %d, stderr:\n%s", r.Code, r.Stderr)
	}
	k, err := kong.New(app.New(), app.Options...)
	if err != nil {
		t.Fatalf("building the grammar: %v", err)
	}
	pages := map[string]string{}
	for _, path := range commandPaths(k.Model.Node, nil) {
		args := append(slices.Clone(path), "--help")
		r := Run(app, args...)
		if r.Code != 0 || r.Stderr != "" {
			t.Errorf("%s: exit %d, stderr:\n%s", strings.Join(args, " "), r.Code, r.Stderr)
			continue
		}
		pages[strings.Join(path, " ")] = r.Stdout
	}
	return pages
}

func commandPaths(n *kong.Node, prefix []string) [][]string {
	out := [][]string{prefix}
	for _, c := range n.Children {
		if !c.Hidden {
			out = append(out, commandPaths(c, append(slices.Clone(prefix), c.Name))...)
		}
	}
	return out
}

// Golden compares every help page of app with dir/help.txt (the root) or
// dir/help_<path>.txt, and reports golden files no page produced, so a renamed
// or removed command shows up as well as a changed one. Run the test with
// CLIKIT_UPDATE=1 to rewrite the files from the pages.
func Golden(t testing.TB, app clikit.App, dir string) {
	t.Helper()
	compare(t, dir, helpPages(t, app))
}

func compare(t testing.TB, dir string, pages map[string]string) {
	t.Helper()
	update := os.Getenv("CLIKIT_UPDATE") == "1"
	owner := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(pages)) {
		name := goldenName(key)
		if other, ok := owner[name]; ok {
			t.Fatalf("help for %q and %q would share golden file %s; rename one command", other, key, name)
		}
		owner[name] = key
	}
	for name, key := range owner {
		file := filepath.Join(dir, name)
		page := pages[key]
		if update {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(page), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		golden, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("help for %q has no golden file %s; run with CLIKIT_UPDATE=1 to create it", key, file)
			continue
		}
		if string(golden) != page {
			t.Errorf("help for %q differs from %s (CLIKIT_UPDATE=1 accepts it)\n--- got\n%s--- want\n%s", key, file, page, golden)
		}
	}
	existing, _ := filepath.Glob(filepath.Join(dir, "help*.txt"))
	for _, file := range existing {
		if _, ok := owner[filepath.Base(file)]; ok {
			continue
		}
		if update {
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			continue
		}
		t.Errorf("%s belongs to no command; remove it, or run with CLIKIT_UPDATE=1", file)
	}
}

func goldenName(key string) string {
	if key == "" {
		return "help.txt"
	}
	return "help_" + strings.ReplaceAll(key, " ", "_") + ".txt"
}
