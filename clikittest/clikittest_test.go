package clikittest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/qiushiyan/clikit"
)

// recordingTB keeps the failures compare reports instead of failing this test.
type recordingTB struct {
	testing.TB
	errs  []string
	fatal string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.fatal = fmt.Sprintf(format, args...)
	panic(r)
}

func recordCompare(t *testing.T, dir string, pages map[string]string) (tb *recordingTB) {
	tb = &recordingTB{TB: t}
	defer func() {
		if r := recover(); r != nil && r != tb {
			panic(r)
		}
	}()
	compare(tb, dir, pages)
	return tb
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCompareReportsDrift(t *testing.T) {
	t.Setenv("CLIKIT_UPDATE", "")
	dir := t.TempDir()
	writeFile(t, dir, "help.txt", "Usage: tool <command>\n")
	writeFile(t, dir, "help_list.txt", "Usage: tool list --old\n")
	writeFile(t, dir, "help_gone.txt", "Usage: tool gone\n")

	tb := recordCompare(t, dir, map[string]string{
		"":     "Usage: tool <command>\n",
		"list": "Usage: tool list --new\n",
		"add":  "Usage: tool add <path>\n",
	})
	got := strings.Join(tb.errs, "\n")
	for _, want := range []string{`help for "list" differs`, `help for "add" has no golden file`, "help_gone.txt belongs to no command"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if len(tb.errs) != 3 {
		t.Errorf("want 3 failures, got %d:\n%s", len(tb.errs), got)
	}
}

func TestCompareRefusesSharedFileNames(t *testing.T) {
	t.Setenv("CLIKIT_UPDATE", "1")
	dir := t.TempDir()
	tb := recordCompare(t, dir, map[string]string{"a b": "one\n", "a_b": "two\n"})
	if !strings.Contains(tb.fatal, "would share golden file help_a_b.txt") {
		t.Fatalf("fatal: %q", tb.fatal)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*")); len(files) != 0 {
		t.Fatalf("wrote %v before refusing", files)
	}
}

// CLIKIT_UPDATE=1, and only 1, rewrites the golden files: stale content is
// replaced, files no page produced are removed, and the next comparison passes.
func TestCompareUpdatesOnlyOnOne(t *testing.T) {
	dir := t.TempDir()
	pages := map[string]string{"": "root\n", "repo add": "add\n"}
	t.Setenv("CLIKIT_UPDATE", "0")
	recordCompare(t, dir, pages)
	if _, err := os.Stat(filepath.Join(dir, "help.txt")); err == nil {
		t.Fatal("CLIKIT_UPDATE=0 wrote a golden file")
	}

	t.Setenv("CLIKIT_UPDATE", "1")
	writeFile(t, dir, "help.txt", "stale\n")
	writeFile(t, dir, "help_gone.txt", "x")
	compare(t, dir, pages)
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	if got := strings.Join(names, ","); got != "help.txt,help_repo_add.txt" {
		t.Fatalf("files after update: %s", got)
	}

	t.Setenv("CLIKIT_UPDATE", "")
	if tb := recordCompare(t, dir, pages); len(tb.errs) > 0 || tb.fatal != "" {
		t.Fatalf("the updated files do not match the pages: %v %s", tb.errs, tb.fatal)
	}
}

type chatty struct{}

func (chatty) Run(ctx *kong.Context) error {
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() { fmt.Fprintf(ctx.Stdout, "line %d\n", i) })
	}
	wg.Wait()
	return nil
}

// Run captures a command that writes from several goroutines; under -race this
// fails if the capture is not locked.
func TestRunCapturesConcurrentWrites(t *testing.T) {
	app := clikit.App{Name: "c", Description: "c writes from goroutines.", New: func() any { return &chatty{} }}
	if r := Run(app); r.Code != 0 || strings.Count(r.Stdout, "\n") != 10 {
		t.Fatalf("%+v", r)
	}
}
