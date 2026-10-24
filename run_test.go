package clikit_test

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/qiushiyan/clikit"
	"github.com/qiushiyan/clikit/clikittest"
)

// journal records what ran, so a test can assert that help ran nothing.
type journal struct {
	mu       sync.Mutex
	ran      []string
	provided int
}

func (j *journal) add(s string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.ran = append(j.ran, s)
}

type deps struct{ home string }

type cli struct {
	JSON bool   `help:"Print one JSON object instead of text."`
	Home string `help:"Root of the registry." env:"CLIKIT_TEST_HOME" default:"~/.local/state/tool"`

	List   listCmd   `cmd:"" help:"List the registered repositories."`
	Repo   repoCmd   `cmd:"" help:"Manage registered repositories."`
	Settle settleCmd `cmd:"" help:"Settle a file both machines changed."`
	Fail   failCmd   `cmd:"" help:"Fail the way <how> says."`
	Echo   echoCmd   `cmd:"" help:"Copy stdin to stdout."`
	Peer   peerCmd   `cmd:"" hidden:"" help:"Internal: answer the other machine."`
}

func (cli) Help() string {
	return "A registry of repositories kept in step.\n\n  Indented lines stay as written."
}

func (cli) HelpSections() []clikit.Section {
	return []clikit.Section{
		{Title: "Output", Body: "--json prints one object; text otherwise.", Shared: true},
		{Title: "Examples", Body: "tool list\ntool repo add ~/dev/gwt"},
	}
}

type listCmd struct {
	Fetch bool `help:"Fetch remotes first."`
	Limit int  `short:"n" help:"Show at most this many."`
}

// AfterApply reports a usage error the tags cannot express, from a hook.
func (c *listCmd) AfterApply(j *journal) error {
	if c.Limit < 0 {
		return clikit.Usage(errors.New("--limit cannot be negative"))
	}
	j.add("list:AfterApply")
	return nil
}

func (c *listCmd) Run(j *journal, d *deps) error {
	j.add("list " + d.home)
	return nil
}

type repoCmd struct {
	Add    repoAddCmd    `cmd:"" help:"Register a repository by its path."`
	Remove repoRemoveCmd `cmd:"" help:"Forget repositories; their files stay."`
}

type repoAddCmd struct {
	Path string `arg:"" help:"Directory of the checkout to register."`
	Name string `help:"Name to register it under." placeholder:"NAME"`
	Role string `help:"What the repository is for." enum:"tool,lib,notes" default:"tool" env:"CLIKIT_TEST_ROLE"`
}

func (c *repoAddCmd) Run(j *journal) error {
	j.add("repo add " + c.Path + " " + c.Role)
	return nil
}

func (c *repoAddCmd) HelpSections() []clikit.Section {
	return []clikit.Section{{Title: "Examples", Body: "tool repo add ~/dev/gwt --role tool"}}
}

type repoRemoveCmd struct {
	Names []string `arg:"" help:"Names of the repositories to forget."`
	Force bool     `short:"f" help:"Forget them even with unpushed work."`
}

func (c *repoRemoveCmd) Run(j *journal) error {
	j.add("repo remove " + strings.Join(c.Names, ","))
	return nil
}

type settleCmd struct {
	Target  string `arg:"" help:"Path to settle."`
	Keep    string `xor:"how" required:"" placeholder:"MACHINE" help:"Keep this machine's copy."`
	Delete  bool   `xor:"how" required:"" help:"Delete the file on both machines."`
	Retries int    `help:"Attempts before giving up." env:"CLIKIT_TEST_RETRIES" default:"3"`
}

func (c *settleCmd) Run(j *journal) error {
	j.add("settle " + c.Target)
	return nil
}

type failCmd struct {
	How string `arg:"" enum:"exit,fail,usage,error" help:"Which failure to return."`
}

func (c *failCmd) Run(ctx *kong.Context) error {
	switch c.How {
	case "exit":
		fmt.Fprintln(ctx.Stderr, "attention: 2 repositories behind")
		return clikit.Exit(5)
	case "fail":
		return clikit.Fail(5, errors.New("2 repositories behind"))
	case "usage":
		return clikit.Usage(errors.New("<how> usage needs a second machine"))
	}
	return errors.New("boom")
}

type echoCmd struct{}

func (echoCmd) Run(ctx *kong.Context, stdin io.Reader) error {
	_, err := io.Copy(ctx.Stdout, stdin)
	return err
}

type peerCmd struct{}

func (*peerCmd) Run() error { return nil }

// newApp is the tool as main would define it: one value, with a fresh journal
// so a test can see what ran. The provider takes the parsed root, so the
// options capture no grammar instance.
func newApp() (clikit.App, *journal) {
	j := &journal{}
	return clikit.App{
		Name:        "tool",
		Description: "tool keeps a registry of repositories in step.",
		New:         func() any { return &cli{} },
		Options: []kong.Option{
			kong.Bind(j),
			kong.BindToProvider(func(c *cli) (*deps, error) {
				j.mu.Lock()
				j.provided++
				j.mu.Unlock()
				return &deps{home: c.Home}, nil
			}),
		},
		ExitCodes: map[int]string{5: "done, but some repositories need attention"},
	}, j
}

func run(args ...string) (clikittest.Result, *journal) {
	app, j := newApp()
	return clikittest.Run(app, args...), j
}

func TestHelpRunsNothing(t *testing.T) {
	for _, args := range [][]string{
		{"--help"},
		{"list", "--help"},
		{"list", "--fetch", "-h"},
		{"repo", "add", "--help"}, // its required <path> is missing
		{"settle", "--help"},      // a required xor group is unset
		{"repo", "--help"},
	} {
		r, j := run(args...)
		if r.Code != 0 || r.Stderr != "" {
			t.Errorf("%v: exit %d, stderr %q", args, r.Code, r.Stderr)
		}
		if !strings.HasPrefix(r.Stdout, "Usage: tool") {
			t.Errorf("%v: stdout does not start with the synopsis:\n%s", args, r.Stdout)
		}
		if len(j.ran) > 0 || j.provided > 0 {
			t.Errorf("%v: help ran %v and called the provider %d times", args, j.ran, j.provided)
		}
	}
}

func TestHelpIgnoresBrokenEnvironment(t *testing.T) {
	t.Setenv("CLIKIT_TEST_RETRIES", "many")
	if r, _ := run("settle", "--help"); r.Code != 0 || r.Stderr != "" {
		t.Fatalf("exit %d, stderr %q", r.Code, r.Stderr)
	}
	r, j := run("settle", "x", "--delete")
	if r.Code != 1 || !strings.Contains(r.Stderr, "CLIKIT_TEST_RETRIES") || len(j.ran) > 0 {
		t.Fatalf("a bad environment value is a failure, not usage: exit %d, ran %v, stderr %q", r.Code, j.ran, r.Stderr)
	}
}

type envChoice struct {
	Choice string `arg:"" optional:"" default:"a" enum:"a,b" env:"CLIKIT_TEST_FIRST,CLIKIT_TEST_SECOND" help:"Choice."`
}

func (*envChoice) Run() error { return nil }

// A bad enum value from the environment is a failure, not a usage error, and
// only the value Kong would use is judged.
func TestEnumFromEnvironmentIsAFailure(t *testing.T) {
	t.Run("variable", func(t *testing.T) {
		t.Setenv("CLIKIT_TEST_ROLE", "nope")
		r, j := run("repo", "add", "/tmp")
		if r.Code != 1 || len(j.ran) > 0 || strings.Count(r.Stderr, "\n") != 1 {
			t.Fatalf("exit %d, ran %v, stderr %q", r.Code, j.ran, r.Stderr)
		}
		for _, want := range []string{"$CLIKIT_TEST_ROLE", `"nope"`, "--role", "tool, lib, notes"} {
			if !strings.Contains(r.Stderr, want) {
				t.Errorf("stderr %q does not name %s", r.Stderr, want)
			}
		}
	})
	t.Run("flag in argv wins", func(t *testing.T) {
		t.Setenv("CLIKIT_TEST_ROLE", "nope")
		if r, j := run("repo", "add", "/tmp", "--role", "lib"); r.Code != 0 || strings.Join(j.ran, "|") != "repo add /tmp lib" {
			t.Fatalf("exit %d, ran %v, stderr %q", r.Code, j.ran, r.Stderr)
		}
		// and a bad value in argv is still a usage error
		if r, _ := run("repo", "add", "/tmp", "--role", "bad"); r.Code != 2 {
			t.Fatalf("argv: exit %d", r.Code)
		}
	})
	app := clikit.App{Name: "e", Description: "e takes a choice.", New: func() any { return &envChoice{} }}
	t.Run("positional in argv wins", func(t *testing.T) {
		t.Setenv("CLIKIT_TEST_FIRST", "bad")
		if r := clikittest.Run(app, "b"); r.Code != 0 {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("first variable set wins", func(t *testing.T) {
		t.Setenv("CLIKIT_TEST_FIRST", "a")
		t.Setenv("CLIKIT_TEST_SECOND", "bad")
		if r := clikittest.Run(app); r.Code != 0 {
			t.Fatalf("%+v", r)
		}
	})
}

// One App value serves every invocation, in turn and at once: nothing parsed
// into one run is seen by another, and Run leaves App.Options as it was.
func TestAppIsReusable(t *testing.T) {
	app, j := newApp()
	app.Options = slices.Grow(app.Options, 8) // room an append in Run must not share
	clikittest.Run(app, "--home", "/first", "list")
	clikittest.Run(app, "list")
	if got := strings.Join(j.ran, "|"); got != "list:AfterApply|list /first|list:AfterApply|list ~/.local/state/tool" {
		t.Fatalf("ran %s", got)
	}

	j.ran = nil
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() { clikittest.Run(app, "--home", fmt.Sprint("/", i), "list") })
	}
	wg.Wait()
	for i := range 8 {
		if !slices.Contains(j.ran, fmt.Sprint("list /", i)) {
			t.Errorf("run %d did not see its own --home: ran %v", i, j.ran)
		}
	}
}

func TestStdin(t *testing.T) {
	app, _ := newApp()
	app.Stdin = strings.NewReader("from stdin\n")
	if r := clikittest.Run(app, "echo"); r.Code != 0 || r.Stdout != "from stdin\n" {
		t.Fatalf("%+v", r)
	}
}

// Invalid argv exits with the usage code after an error line naming what is
// wrong, the synopsis its command's help opens with, and that help's command.
func TestUsageErrors(t *testing.T) {
	cases := []struct {
		args    []string
		command []string // the command the error concerns
		names   []string // what the error line names
	}{
		{[]string{"lst"}, nil, []string{"lst"}},
		{[]string{"repo", "add"}, []string{"repo", "add"}, []string{"<path>"}},
		{[]string{"repo", "add", "/tmp", "--role", "nope"}, []string{"repo", "add"}, []string{"--role", "nope"}},
		{[]string{"list", "-n", "many"}, []string{"list"}, []string{"--limit", "many"}},
		{[]string{"settle", "x"}, []string{"settle"}, []string{"--keep=MACHINE", "--delete"}},
		{[]string{"settle", "x", "--keep", "mini", "--delete"}, []string{"settle"}, []string{"--keep", "--delete"}},
		{[]string{"fail", "usage"}, []string{"fail"}, []string{"<how> usage needs a second machine"}},
		// Usage from a hook arrives inside Kong's parse error, which carries the command.
		{[]string{"list", "--limit=-1"}, []string{"list"}, []string{"--limit cannot be negative"}},
	}
	for _, c := range cases {
		r, j := run(c.args...)
		if r.Code != 2 || r.Stdout != "" || len(j.ran) > 0 {
			t.Errorf("%v: exit %d, ran %v, stdout %q", c.args, r.Code, j.ran, r.Stdout)
		}
		lines := strings.Split(strings.TrimSuffix(r.Stderr, "\n"), "\n")
		if len(lines) != 3 || !strings.HasPrefix(lines[0], "tool: error: ") {
			t.Errorf("%v: stderr:\n%s", c.args, r.Stderr)
			continue
		}
		for _, name := range c.names {
			if !strings.Contains(lines[0], name) {
				t.Errorf("%v: %q does not name %s", c.args, lines[0], name)
			}
		}
		help, _ := run(append(slices.Clone(c.command), "--help")...)
		if synopsis, _, _ := strings.Cut(help.Stdout, "\n"); lines[1] != synopsis {
			t.Errorf("%v: %q is not the synopsis of its help, %q", c.args, lines[1], synopsis)
		}
		if want := strconv.Quote(strings.Join(append([]string{"tool"}, c.command...), " ") + " --help"); !strings.Contains(lines[2], want) {
			t.Errorf("%v: %q does not point at %s", c.args, lines[2], want)
		}
	}
}

// exitCodesListed are the codes a help page's Exit codes section lists.
func exitCodesListed(page string) []string {
	_, section, _ := strings.Cut(page, "\nExit codes:\n")
	section, _, _ = strings.Cut(section, "\n\n")
	var codes []string
	for line := range strings.Lines(section) {
		if fields := strings.Fields(line); len(fields) > 0 {
			codes = append(codes, fields[0])
		}
	}
	return codes
}

func TestUsageCodeIsTheToolsOwn(t *testing.T) {
	app, _ := newApp()
	app.UsageCode, app.FailureCode = 3, 4
	if r := clikittest.Run(app, "lst"); r.Code != 3 {
		t.Errorf("usage: exit %d", r.Code)
	}
	if r := clikittest.Run(app, "fail", "error"); r.Code != 4 {
		t.Errorf("failure: exit %d", r.Code)
	}
	if got := exitCodesListed(clikittest.Run(app, "--help").Stdout); !slices.Equal(got, []string{"0", "3", "4", "5"}) {
		t.Errorf("help lists exit codes %v", got)
	}
}

// A command picks its outcome: Exit after reporting it itself, Fail for Run to
// report, or any other error for the failure code. Usage is TestUsageErrors'.
func TestCommandOutcomes(t *testing.T) {
	for _, c := range []struct {
		how    string
		code   int
		stderr string
	}{
		{"exit", 5, "attention: 2 repositories behind\n"},
		{"fail", 5, "tool: error: 2 repositories behind\n"},
		{"error", 1, "tool: error: boom\n"},
	} {
		if r, _ := run("fail", c.how); r.Code != c.code || r.Stderr != c.stderr || r.Stdout != "" {
			t.Errorf("fail %s: exit %d, stdout %q, stderr %q", c.how, r.Code, r.Stdout, r.Stderr)
		}
	}
	err := errors.New("x")
	for _, c := range []struct {
		name      string
		got, want error
	}{
		{"Exit(0)", clikit.Exit(0), nil},
		{"Usage(nil)", clikit.Usage(nil), nil},
		{"Fail(5, nil)", clikit.Fail(5, nil), nil},
		{"Fail(0, err)", clikit.Fail(0, err), err},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

// A help page that cannot be written is a failure, whatever the writer's
// error wraps.
func TestHelpWriteFailure(t *testing.T) {
	for _, err := range []error{errors.New("broken pipe"), clikit.Usage(errors.New("broken pipe"))} {
		app, _ := newApp()
		var stderr strings.Builder
		app.Stdout, app.Stderr = failingWriter{err}, &stderr
		code := app.Run([]string{"--help"})
		if got := stderr.String(); code != 1 || strings.Count(got, "\n") != 1 || !strings.Contains(got, "writing help") || !strings.Contains(got, "broken pipe") {
			t.Errorf("writer error %T: exit %d, stderr %q", err, code, got)
		}
	}
}

type quiet struct {
	Go bool `help:"Say it."`
}

func (*quiet) Run(ctx *kong.Context) error {
	fmt.Fprintln(ctx.Stdout, "RAN")
	return nil
}

// Options a tool passes cannot take help's ending, writers or printer away,
// and a PostBuild of the tool's own already sees App's writers.
func TestOptionsCannotUndoHelp(t *testing.T) {
	var stdout, stderr, stolen strings.Builder
	var seen io.Writer
	app := clikit.App{
		Name: "q", Description: "q says something.", New: func() any { return &quiet{} },
		Stdout: &stdout, Stderr: &stderr,
		Options: []kong.Option{
			kong.Writers(&stolen, &stolen),
			kong.Exit(func(int) {}),
			kong.Help(kong.DefaultHelpPrinter),
			kong.PostBuild(func(k *kong.Kong) error {
				seen = k.Stdout
				return nil
			}),
			kong.PostBuild(func(k *kong.Kong) error {
				k.Stdout, k.Exit = &stolen, func(int) {}
				return nil
			}),
		},
	}
	code := app.Run([]string{"--help"})
	if code != 0 || !strings.HasPrefix(stdout.String(), "Usage: q [flags]\n") || strings.Contains(stdout.String(), "RAN") || stolen.Len() != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q, stolen %q", code, stdout.String(), stderr.String(), stolen.String())
	}
	if seen != io.Writer(&stdout) {
		t.Errorf("a PostBuild saw %T, not App.Stdout", seen)
	}

	app.Options = []kong.Option{kong.NoDefaultHelp()}
	r := clikittest.Run(app, "--go")
	if r.Code != 1 || !strings.Contains(r.Stderr, "kong.NoDefaultHelp removes --help") {
		t.Fatalf("NoDefaultHelp: exit %d, stderr %q", r.Code, r.Stderr)
	}
}

type panicky struct{}

func (*panicky) Run() error { panic("command panic") }

// Run recovers only its own exit signal: a command's panic is a bug, and it
// reaches the caller.
func TestCommandPanicEscapes(t *testing.T) {
	defer func() {
		if r := recover(); r != "command panic" {
			t.Fatalf("recovered %v", r)
		}
	}()
	clikittest.Run(clikit.App{Name: "p", Description: "p panics.", New: func() any { return &panicky{} }})
	t.Fatal("Run returned")
}

type fileCheck struct{}

func (fileCheck) Run(ctx *kong.Context) error {
	if _, ok := ctx.Stdout.(*os.File); !ok {
		return fmt.Errorf("stdout is %T", ctx.Stdout)
	}
	return nil
}

// A command sees the configured writer itself, so terminal detection on an
// *os.File still works.
func TestStdoutKeepsItsIdentity(t *testing.T) {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	var stderr strings.Builder
	app := clikit.App{Name: "f", Description: "f checks its stdout.", New: func() any { return &fileCheck{} }, Stdout: devnull, Stderr: &stderr}
	if code := app.Run(nil); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
}

func TestHelpCommand(t *testing.T) {
	app, _ := newApp()
	for _, path := range [][]string{nil, {"repo"}, {"repo", "add"}} {
		want := clikittest.Run(app, append(slices.Clone(path), "--help")...)
		got := clikittest.Run(app, append([]string{"help"}, path...)...)
		if got != want || got.Code != 0 {
			t.Errorf("help %v differs from %v --help:\n%s\n---\n%s", path, path, got.Stdout, want.Stdout)
		}
	}
	if r := clikittest.Run(app, "help", "nope"); r.Code != 2 || !strings.Contains(r.Stderr, "unexpected argument nope") {
		t.Errorf("help nope: %+v", r)
	}
}

type ownHelp struct {
	Help helpCmd `cmd:"" help:"Explain a topic."`
}

type helpCmd struct {
	Topic string `arg:"" optional:"" help:"Topic to explain."`
}

func (c *helpCmd) Run(ctx *kong.Context) error {
	fmt.Fprintln(ctx.Stdout, "topic", c.Topic)
	return nil
}

func TestOwnHelpCommandWins(t *testing.T) {
	app := clikit.App{Name: "t", Description: "t explains.", New: func() any { return &ownHelp{} }}
	if r := clikittest.Run(app, "help", "flags"); r.Code != 0 || r.Stdout != "topic flags\n" {
		t.Fatalf("%+v", r)
	}
}

// A test replaces a binding by appending an option of the same type to a copy
// of App.Options: Kong keeps one binding per type, and the later one wins.
func TestLaterBindingReplacesEarlier(t *testing.T) {
	app, j := newApp()
	app.Options = append(slices.Clone(app.Options), kong.BindToProvider(func() (*deps, error) {
		return &deps{home: "fake"}, nil
	}))
	if r := clikittest.Run(app, "list"); r.Code != 0 || !slices.Contains(j.ran, "list fake") {
		t.Fatalf("exit %d, ran %v, stderr %q", r.Code, j.ran, r.Stderr)
	}
}
