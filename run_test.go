package clikit_test

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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

func (c *listCmd) AfterApply(j *journal) error {
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
	How string `arg:"" enum:"exit,usage,error" help:"Which failure to return."`
}

func (c *failCmd) Run(ctx *kong.Context) error {
	switch c.How {
	case "exit":
		fmt.Fprintln(ctx.Stderr, "attention: 2 repositories behind")
		return clikit.Exit(5)
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

func (c *peerCmd) Run(j *journal) error {
	j.add("peer")
	return nil
}

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

func TestEnumFromEnvironmentIsAFailure(t *testing.T) {
	t.Setenv("CLIKIT_TEST_ROLE", "nope")
	r, j := run("repo", "add", "/tmp")
	want := `tool: error: $CLIKIT_TEST_ROLE="nope" is not a valid flag --role: must be one of tool, lib, notes` + "\n"
	if r.Code != 1 || r.Stderr != want || len(j.ran) > 0 {
		t.Fatalf("exit %d, ran %v, stderr %q", r.Code, j.ran, r.Stderr)
	}
	// argv wins over the variable
	if r, j := run("repo", "add", "/tmp", "--role", "lib"); r.Code != 0 || strings.Join(j.ran, "|") != "repo add /tmp lib" {
		t.Fatalf("argv over env: exit %d, ran %v, stderr %q", r.Code, j.ran, r.Stderr)
	}
	// and an invalid value in argv is still a usage error
	if r, _ := run("repo", "add", "/tmp", "--role", "nope"); r.Code != 2 {
		t.Fatalf("argv: exit %d", r.Code)
	}
}

func TestRunSelectsCommand(t *testing.T) {
	r, j := run("repo", "add", "/tmp", "--role", "lib")
	if r.Code != 0 || r.Stderr != "" || strings.Join(j.ran, "|") != "repo add /tmp lib" {
		t.Fatalf("exit %d, ran %v, stderr %q", r.Code, j.ran, r.Stderr)
	}
	r, j = run("--home", "/srv", "list")
	if r.Code != 0 || strings.Join(j.ran, "|") != "list:AfterApply|list /srv" || j.provided != 1 {
		t.Fatalf("exit %d, ran %v, provided %d", r.Code, j.ran, j.provided)
	}
	r, j = run("peer")
	if r.Code != 0 || strings.Join(j.ran, "|") != "peer" {
		t.Fatalf("a hidden command still runs: exit %d, ran %v", r.Code, j.ran)
	}
}

// One App value serves every invocation: nothing parsed into one run is seen
// by the next.
func TestAppIsReusable(t *testing.T) {
	app, j := newApp()
	clikittest.Run(app, "--home", "/first", "list")
	clikittest.Run(app, "--home", "/second", "list")
	clikittest.Run(app, "list")
	if got := strings.Join(j.ran, "|"); got != "list:AfterApply|list /first|list:AfterApply|list /second|list:AfterApply|list ~/.local/state/tool" {
		t.Fatalf("ran %s", got)
	}
}

func TestStdin(t *testing.T) {
	app, _ := newApp()
	app.Stdin = strings.NewReader("from stdin\n")
	if r := clikittest.Run(app, "echo"); r.Code != 0 || r.Stdout != "from stdin\n" {
		t.Fatalf("%+v", r)
	}
}

func TestUsageErrors(t *testing.T) {
	cases := []struct {
		args          []string
		message, line string
	}{
		{[]string{"lst"}, `tool: error: unexpected argument lst, did you mean "list"?`, "Usage: tool <command> [flags]"},
		{[]string{"repo", "add"}, `tool: error: expected "<path>"`, "Usage: tool repo add <path> [flags]"},
		{[]string{"repo", "add", "/tmp", "--role", "nope"}, `tool: error: --role must be one of "tool","lib","notes" but got "nope"`, "Usage: tool repo add <path> [flags]"},
		{[]string{"list", "-n", "many"}, `tool: error: --limit: expected a valid 64 bit int but got "many"`, "Usage: tool list [flags]"},
		{[]string{"settle", "x"}, "tool: error: missing flags: --keep=MACHINE or --delete", "Usage: tool settle (--keep=MACHINE | --delete) <target> [flags]"},
		{[]string{"settle", "x", "--keep", "mini", "--delete"}, "tool: error: --keep and --delete can't be used together", "Usage: tool settle (--keep=MACHINE | --delete) <target> [flags]"},
		{[]string{"fail", "usage"}, "tool: error: <how> usage needs a second machine", "Usage: tool fail <how> [flags]"},
	}
	for _, c := range cases {
		r, j := run(c.args...)
		if r.Code != 2 || r.Stdout != "" || len(j.ran) > 0 {
			t.Errorf("%v: exit %d, ran %v, stdout %q", c.args, r.Code, j.ran, r.Stdout)
		}
		lines := strings.Split(strings.TrimSpace(r.Stderr), "\n")
		if len(lines) != 3 || lines[0] != c.message || lines[1] != c.line || !strings.HasSuffix(lines[2], `--help" for details.`) {
			t.Errorf("%v: stderr:\n%s", c.args, r.Stderr)
		}
	}
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
	help := clikittest.Run(app, "--help").Stdout
	for _, want := range []string{"  3    invalid arguments; nothing was run", "  4    failed", "  5    done, but some repositories need attention"} {
		if !strings.Contains(help, want) {
			t.Errorf("exit codes miss %q:\n%s", want, help)
		}
	}
}

func TestCommandOutcomes(t *testing.T) {
	r, _ := run("fail", "exit")
	if r.Code != 5 || r.Stderr != "attention: 2 repositories behind\n" {
		t.Errorf("Exit passes the status through silently: exit %d, stderr %q", r.Code, r.Stderr)
	}
	r, _ = run("fail", "error")
	if r.Code != 1 || r.Stderr != "tool: error: boom\n" {
		t.Errorf("exit %d, stderr %q", r.Code, r.Stderr)
	}
	if clikit.Exit(0) != nil || clikit.Usage(nil) != nil {
		t.Error("Exit(0) and Usage(nil) must be nil")
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestHelpWriteFailure(t *testing.T) {
	app, _ := newApp()
	var stderr strings.Builder
	app.Stdout, app.Stderr = brokenWriter{}, &stderr
	if code := app.Run([]string{"--help"}); code != 1 || stderr.String() != "tool: error: writing help: broken pipe\n" {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
}

type quiet struct {
	Go bool `help:"Say it."`
}

func (*quiet) Run(ctx *kong.Context) error {
	fmt.Fprintln(ctx.Stdout, "RAN")
	return nil
}

// Options a tool passes cannot take help's ending, writers or printer away.
func TestOptionsCannotUndoHelp(t *testing.T) {
	var stolen strings.Builder
	app := clikit.App{
		Name: "q", Description: "q says something.", New: func() any { return &quiet{} },
		Options: []kong.Option{
			kong.Writers(&stolen, &stolen),
			kong.Exit(func(int) {}),
			kong.Help(kong.DefaultHelpPrinter),
			kong.PostBuild(func(k *kong.Kong) error {
				k.Stdout, k.Exit = &stolen, func(int) {}
				return nil
			}),
		},
	}
	r := clikittest.Run(app, "--help")
	if r.Code != 0 || !strings.HasPrefix(r.Stdout, "Usage: q [flags]\n") || strings.Contains(r.Stdout, "RAN") || stolen.Len() != 0 {
		t.Fatalf("exit %d, stdout %q, stolen %q", r.Code, r.Stdout, stolen.String())
	}

	app.Options = []kong.Option{kong.NoDefaultHelp()}
	r = clikittest.Run(app, "--go")
	if r.Code != 1 || !strings.Contains(r.Stderr, "kong.NoDefaultHelp removes --help") {
		t.Fatalf("NoDefaultHelp: exit %d, stderr %q", r.Code, r.Stderr)
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

func TestConcurrentOutput(t *testing.T) {
	app := clikit.App{Name: "c", Description: "c writes from goroutines.", New: func() any { return &chatty{} }}
	r := clikittest.Run(app)
	if r.Code != 0 || strings.Count(r.Stdout, "\n") != 10 {
		t.Fatalf("%+v", r)
	}
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

type badDeclarations struct {
	Name string `help:""`
	Add  struct {
		Path string `arg:""`
	} `cmd:""`
	Mode     string `enum:"a,b" default:"c" help:"Mode."`
	Keep     bool   `xor:"how" required:"" help:"Keep."`
	Delete   bool   `xor:"how" help:"Delete."`
	Internal string `hidden:""`
}

type branching struct {
	File struct {
		File string  `arg:"" help:"File name."`
		Cat  peerCmd `cmd:"" help:"Read it."`
	} `arg:"" help:"File operations."`
}

func TestBadDeclarationsFail(t *testing.T) {
	app := clikit.App{Name: "tool", New: func() any { return &badDeclarations{} }}
	r := clikittest.Run(app, "--help")
	for _, want := range []string{
		"App.Description is empty",
		"flag --name has no help",
		"command add has no help",
		"argument <path> of add has no help",
		`flag --mode defaults to "c", which is not one of its values`,
		`xor group "how" marks only some of --keep, --delete required`,
	} {
		if !strings.Contains(r.Stderr, want) {
			t.Errorf("missing %q in %q", want, r.Stderr)
		}
	}
	if r.Code != 1 || r.Stdout != "" || strings.Contains(r.Stderr, "internal") {
		t.Errorf("exit %d, stdout %q, stderr %q", r.Code, r.Stdout, r.Stderr)
	}

	app = clikit.App{Name: "tool", Description: "tool reads files.", New: func() any { return &branching{} }}
	if r := clikittest.Run(app, "--help"); r.Code != 1 || !strings.Contains(r.Stderr, "branching argument <file> is not supported") {
		t.Errorf("branching: exit %d, stderr %q", r.Code, r.Stderr)
	}
}

type tagV1 struct {
	Name string `help:"Label to show." default:"main"`
}

type tagV2 struct {
	Label string `help:"Label to show." default:"primary"`
}

func (*tagV1) Run() error { return nil }
func (*tagV2) Run() error { return nil }

// A renamed flag and a changed default reach help with no other edit, and the
// old spelling stops parsing.
func TestRenameReachesHelp(t *testing.T) {
	app := clikit.App{Name: "tag", Description: "tag prints a label.", New: func() any { return &tagV1{} }}
	before := clikittest.Run(app, "--help").Stdout
	app.New = func() any { return &tagV2{} }
	after := clikittest.Run(app, "--help").Stdout
	if !strings.Contains(before, "--name=NAME") || !strings.Contains(before, "default: main") {
		t.Fatalf("before:\n%s", before)
	}
	if strings.Contains(after, "--name") || !strings.Contains(after, "--label=LABEL") || !strings.Contains(after, "default: primary") {
		t.Fatalf("after:\n%s", after)
	}
	if r := clikittest.Run(app, "--name", "x"); r.Code != 2 {
		t.Fatalf("old spelling: exit %d, stderr %q", r.Code, r.Stderr)
	}
}

func TestHelpPagesGolden(t *testing.T) {
	app, _ := newApp()
	clikittest.Golden(t, app, "testdata/registry")
}

type single struct {
	Path  string `arg:"" optional:"" help:"File to open; the repository root when omitted."`
	Print bool   `short:"p" help:"Print the URL instead of opening it."`
}

func (single) HelpSections() []clikit.Section {
	return []clikit.Section{{Title: "Examples", Body: "open README.md:10\nopen -p"}}
}

func (single) Run() error { return nil }

func TestSingleCommandTool(t *testing.T) {
	clikittest.Golden(t, clikit.App{
		Name:        "open",
		Description: "open shows the checkout on GitHub.",
		New:         func() any { return &single{} },
		ExitCodes:   map[int]string{0: "opened, or printed with --print"},
	}, "testdata/single")
}

// The rest of Kong's grammar that help has to state: a default command's own
// invocation, passthrough, typed placeholders, required and-groups, and
// required flags in the command list.
type grammar struct {
	Show  showCmd  `cmd:"" default:"withargs" help:"Show the brief a fragment names."`
	Exec  execCmd  `cmd:"" help:"Run a program with the tool's environment."`
	Wait  waitCmd  `cmd:"" help:"Wait for a job."`
	Login loginCmd `cmd:"" help:"Store credentials."`
}

type showCmd struct {
	Fragment string `arg:"" optional:"" help:"Part of a brief's name; the index when omitted."`
	Full     bool   `help:"Print the whole brief."`
}

type execCmd struct {
	Verbose bool     `short:"v" help:"Say what runs."`
	Args    []string `arg:"" passthrough:"" help:"Program and its arguments."`
}

type waitCmd struct {
	Job     string        `arg:"" help:"Job to wait for."`
	Timeout time.Duration `help:"Give up after this long." default:"30s"`
	Every   float64       `help:"Seconds between checks." default:"0.5"`
	Count   []int         `help:"Exit codes that count as done."`
}

type loginCmd struct {
	User     string `required:"" and:"cred" help:"Account name."`
	Password string `and:"cred" help:"Account password."`
}

func (c *showCmd) Run(j *journal) error {
	j.add("show " + c.Fragment + fmt.Sprint(c.Full))
	return nil
}

func (c *execCmd) Run(j *journal) error {
	j.add("exec " + strings.Join(c.Args, " "))
	return nil
}

func (*waitCmd) Run() error  { return nil }
func (*loginCmd) Run() error { return nil }

func TestGrammarHelp(t *testing.T) {
	j := &journal{}
	app := clikit.App{
		Name: "brief", Description: "brief indexes handoff briefs.",
		New:     func() any { return &grammar{} },
		Options: []kong.Option{kong.Bind(j)},
	}
	clikittest.Golden(t, app, "testdata/grammar")

	if r := clikittest.Run(app, "--full", "drift"); r.Code != 0 || strings.Join(j.ran, "|") != "show drifttrue" {
		t.Fatalf("default command: %+v, ran %v", r, j.ran)
	}
	j.ran = nil
	if r := clikittest.Run(app, "exec", "-v", "ls", "-la", "--", "x"); r.Code != 0 || strings.Join(j.ran, "|") != "exec ls -la -- x" {
		t.Fatalf("passthrough keeps flags and --: %+v, ran %v", r, j.ran)
	}
	if r := clikittest.Run(app, "login", "--user", "me"); r.Code != 2 || !strings.Contains(r.Stderr, "Usage: brief login --user=USER --password=PASSWORD\n") {
		t.Fatalf("a required and-group needs every member, and says so: %+v", r)
	}
}

type noteCLI struct {
	Note   noteCmd   `cmd:"" help:"Append a note."`
	Pickup pickupCmd `cmd:"" help:"Pick up a brief."`
	Wait   waitNCmd  `cmd:"" help:"Wait for some jobs."`
	Ask    askCmd    `cmd:"" help:"Resolve a fragment to one brief."`
}

type noteCmd struct {
	Text []string `arg:"" help:"Words of the note."`
}

type pickupCmd struct {
	Mode string `required:"" enum:"build,design" help:"How the first turn runs."`
}

type waitNCmd struct {
	Count int `arg:"" optional:"" help:"How many jobs to wait for."`
}

type askCmd struct {
	Fragment string `arg:"" help:"Part of a brief's name."`
}

type seam interface{ Now() string }

type realClock struct{}

func (realClock) Now() string { return "real" }

type fakeClock struct{}

func (fakeClock) Now() string { return "fake" }

func (c *noteCmd) Run(ctx *kong.Context, clock seam) error {
	fmt.Fprintln(ctx.Stdout, clock.Now(), strings.Join(c.Text, " "))
	return nil
}
func (*pickupCmd) Run() error { return nil }
func (*waitNCmd) Run() error  { return nil }

func (c *askCmd) Run(ctx *kong.Context) error {
	return clikit.Fail(5, fmt.Errorf("%s names two briefs", c.Fragment))
}

func noteApp() clikit.App {
	return clikit.App{
		Name: "jot", Description: "jot keeps notes.",
		New:       func() any { return &noteCLI{} },
		Options:   []kong.Option{kong.BindTo(realClock{}, (*seam)(nil))},
		ExitCodes: map[int]string{5: "the fragment names more than one brief"},
	}
}

// Kong's messages are restated where they mislead, and the flag in an error
// is named exactly as the usage line names it.
func TestUsageMessages(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"pickup"}, []string{"jot: error: missing flags: --mode=MODE", "Usage: jot pickup --mode=MODE"}},
		{[]string{"pickup", "--mode"}, []string{"jot: error: --mode: missing value"}},
		{[]string{"wait", "many"}, []string{`jot: error: <count>: expected a valid 64 bit int but got "many"`}},
		{[]string{"note", "use", "-n", "now"}, []string{"jot: error: unknown flag -n", `Text that starts with "-" goes after --, which ends the flags.`, "Usage: jot note <text> ..."}},
	}
	for _, c := range cases {
		r := clikittest.Run(noteApp(), c.args...)
		if r.Code != 2 {
			t.Errorf("%v: exit %d", c.args, r.Code)
		}
		for _, w := range c.want {
			if !strings.Contains(r.Stderr, w+"\n") {
				t.Errorf("%v: missing line %q in:\n%s", c.args, w, r.Stderr)
			}
		}
		if strings.Contains(r.Stderr, `did you mean "-h"`) {
			t.Errorf("%v: offers help for a mistyped flag:\n%s", c.args, r.Stderr)
		}
	}
	if r := clikittest.Run(noteApp(), "note", "--", "-n", "now"); r.Code != 0 || r.Stdout != "real -n now\n" {
		t.Errorf("text after --: %+v", r)
	}
}

func TestFail(t *testing.T) {
	r := clikittest.Run(noteApp(), "ask", "drift")
	if r.Code != 5 || r.Stderr != "jot: error: drift names two briefs\n" || r.Stdout != "" {
		t.Fatalf("%+v", r)
	}
	if clikit.Fail(5, nil) != nil {
		t.Error("Fail(code, nil) must be nil")
	}
	if err := errors.New("x"); clikit.Fail(0, err) != err {
		t.Error("Fail(0, err) must be err")
	}
	if help := clikittest.Run(noteApp(), "--help").Stdout; !strings.Contains(help, "  5    the fragment names more than one brief\n") {
		t.Errorf("exit codes:\n%s", help)
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

func TestEqualUsageAndFailureCodes(t *testing.T) {
	app := noteApp()
	app.UsageCode, app.FailureCode = 1, 1
	help := clikittest.Run(app, "--help").Stdout
	if !strings.Contains(help, "  1    failed, or invalid arguments (then nothing was run)\n") {
		t.Fatalf("exit codes:\n%s", help)
	}
}

// A test replaces a binding by appending an option of the same type to a copy
// of App.Options: Kong keeps one binding per type, and the later one wins.
func TestLaterBindingReplacesEarlier(t *testing.T) {
	app := noteApp()
	app.Options = append(slices.Clone(app.Options), kong.BindTo(fakeClock{}, (*seam)(nil)))
	if r := clikittest.Run(app, "note", "hi"); r.Stdout != "fake hi\n" {
		t.Fatalf("%+v", r)
	}
	if r := clikittest.Run(noteApp(), "note", "hi"); r.Stdout != "real hi\n" {
		t.Fatalf("the original App changed: %+v", r)
	}
}

// Generated rows wrap at a fixed width; authored text is left as written. The
// golden files are every page the tests above render.
func TestRowsWrap(t *testing.T) {
	files, _ := filepath.Glob("testdata/*/help*.txt")
	if len(files) == 0 {
		t.Fatal("no golden pages under testdata")
	}
	for _, file := range files {
		page, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.SplitSeq(string(page), "\n") {
			if utf8.RuneCountInString(line) > 100 {
				t.Errorf("%s: line over 100 columns: %q", file, line)
			}
		}
	}
}

// Kong checks a group among one command's flags, never requires a flag alone
// in its group, and takes a flag's requirement from its last and group, so a
// group whose checks help could not state is rejected.
type spanningGroup struct {
	Local bool          `xor:"where" required:"" help:"Use the local copy."`
	Fetch spanningFetch `cmd:"" help:"Fetch a copy."`
}

type spanningFetch struct {
	Remote bool `xor:"where" required:"" help:"Use the remote copy."`
}

type tangledGroups struct {
	Solo   string `required:"" and:"solo" help:"Solo."`
	Keep   string `xor:"how" required:"" and:"note" help:"Keep."`
	Delete bool   `xor:"how" required:"" help:"Delete."`
	Note   string `and:"note" help:"Note."`
	X      string `required:"" and:"a" help:"X."`
	Y      string `and:"a,b" help:"Y."`
	Z      string `and:"b" help:"Z."`
}

// Groups Kong checks as declared: optional groups that overlap, a required
// flag in two and groups, and a flag an and group requires inside an optional
// xor group.
type statedGroups struct {
	User     string `xor:"auth" and:"cred" help:"Account name."`
	Password string `and:"cred" help:"Account password."`
	Token    string `xor:"auth" help:"API token."`
	Host     string `required:"" and:"site,port" help:"Host."`
	Path     string `and:"site" help:"Path."`
	Port     int    `and:"port" help:"Port."`
	Mirror   string `xor:"source" and:"mirror" help:"Mirror to copy."`
	Local    string `xor:"source" help:"Local directory."`
	Into     string `required:"" and:"mirror" help:"Destination."`
}

func (*statedGroups) Run() error { return nil }

func TestFlagGroups(t *testing.T) {
	app := clikit.App{Name: "g", Description: "g groups flags.", New: func() any { return &spanningGroup{} }}
	r := clikittest.Run(app, "--help")
	if want := `xor group "where" spans --local, --remote of fetch; Kong checks a group within one command`; r.Code != 1 || !strings.Contains(r.Stderr, want) || strings.Contains(r.Stderr, "has only") {
		t.Errorf("spanning: exit %d, stderr %q", r.Code, r.Stderr)
	}

	app.New = func() any { return &tangledGroups{} }
	r = clikittest.Run(app, "--help")
	for _, want := range []string{
		`required and group "solo" has only --solo; Kong never requires a flag alone in its group`,
		`flag --keep is in required xor group "how" and in and group "note"; Kong then demands all of "note" even when another flag of "how" is given`,
		`flag --y is in required and group "a" and in and group "b" without being required itself`,
	} {
		if !strings.Contains(r.Stderr, want) {
			t.Errorf("missing %q in %q", want, r.Stderr)
		}
	}
	if r.Code != 1 {
		t.Errorf("tangled: exit %d", r.Code)
	}

	app.New = func() any { return &statedGroups{} }
	help := clikittest.Run(app, "--help")
	for _, want := range []string{
		"At most one of --user, --token.",
		"All or none of --user, --password.",
		"At most one of --mirror, --local.",
		"Path (required).",
		"Port (required).",
		"Mirror to copy (required).",
	} {
		if !strings.Contains(help.Stdout, want) {
			t.Errorf("stated: missing %q in:\n%s%s", want, help.Stdout, help.Stderr)
		}
	}
	if r := clikittest.Run(app, "--host=h", "--path=p", "--mirror=m", "--into=d"); r.Code != 2 || !strings.Contains(r.Stderr, "--port") {
		t.Errorf("Kong requires --port as help says: %+v", r)
	}
	if r := clikittest.Run(app, "--host=h", "--path=p", "--port=1", "--mirror=m", "--into=d"); r.Code != 0 {
		t.Errorf("every required flag given: %+v", r)
	}
}
