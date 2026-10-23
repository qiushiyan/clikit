package clikittest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestCompareUpdatesOnlyOnOne(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIKIT_UPDATE", "0")
	recordCompare(t, dir, map[string]string{"": "root\n"})
	if _, err := os.Stat(filepath.Join(dir, "help.txt")); err == nil {
		t.Fatal("CLIKIT_UPDATE=0 wrote a golden file")
	}

	t.Setenv("CLIKIT_UPDATE", "1")
	writeFile(t, dir, "help_gone.txt", "x")
	compare(t, dir, map[string]string{"": "root\n", "repo add": "add\n"})
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	if got := strings.Join(names, ","); got != "help.txt,help_repo_add.txt" {
		t.Fatalf("files after update: %s", got)
	}
}
