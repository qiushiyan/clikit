package clikit_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/qiushiyan/clikit"
	"github.com/qiushiyan/clikit/clikittest"
)

// Rendering is pinned by the golden pages under testdata, one directory per
// fixture tool; the tests here add what bytes cannot say: that the parser
// accepts and rejects what a page states.

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
		UsageCode:   1, // the same as the failure code, so help lists one row for both
	}, "testdata/single")
}

// The rest of Kong's grammar that help has to state: a default command's own
// invocation (its prose only on its own page), passthrough, typed
// placeholders, required and-groups, required flags in the command list, a
// command that runs with or without one below it, and authored text and long
// words left whole.
type grammar struct {
	Show   showCmd   `cmd:"" default:"withargs" help:"Show the brief a fragment names."`
	Exec   execCmd   `cmd:"" help:"Run a program with the tool's environment."`
	Wait   waitCmd   `cmd:"" help:"Wait for a job."`
	Login  loginCmd  `cmd:"" help:"Store credentials."`
	Config configCmd `cmd:"" help:"Print the configuration, or change one key."`
}

func (grammar) HelpSections() []clikit.Section {
	return []clikit.Section{{Title: "Notes", Body: "A brief is one Markdown file whose name is its slug and whose folder is its domain; nothing else indexes it.\n\nAuthored lines stay as written, however long."}}
}

type showCmd struct {
	Fragment string `arg:"" optional:"" help:"Part of a brief's name; the index when omitted."`
	Full     bool   `help:"Print the whole brief."`
}

func (showCmd) Help() string {
	return "The card a reader needs before picking the brief up."
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
	Hook    string        `help:"Address to call when it is done: https://hooks.example.com/brief/v1/jobs/wait/notify-when-every-check-has-passed."`
}

type loginCmd struct {
	User     string `required:"" and:"cred" help:"Account name."`
	Password string `and:"cred" help:"Account password."`
}

type configCmd struct {
	Set configSetCmd `cmd:"" help:"Set one key."`
}

type configSetCmd struct {
	Key   string `arg:"" help:"Key to set."`
	Value string `arg:"" help:"Its new value."`
}

func (c *showCmd) Run(j *journal) error {
	j.add("show " + c.Fragment + fmt.Sprint(c.Full))
	return nil
}

func (c *execCmd) Run(j *journal) error {
	j.add("exec " + strings.Join(c.Args, " "))
	return nil
}

func (*configCmd) Run(j *journal) error {
	j.add("config")
	return nil
}

func (*waitCmd) Run() error      { return nil }
func (*loginCmd) Run() error     { return nil }
func (*configSetCmd) Run() error { return nil }

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
	if r := clikittest.Run(app); r.Code != 0 || strings.Join(j.ran, "|") != "show false" {
		t.Fatalf("brief [<command>] runs alone: %+v, ran %v", r, j.ran)
	}
	j.ran = nil
	if r := clikittest.Run(app, "exec", "-v", "ls", "-la", "--", "x"); r.Code != 0 || strings.Join(j.ran, "|") != "exec ls -la -- x" {
		t.Fatalf("passthrough keeps flags and --: %+v, ran %v", r, j.ran)
	}
	j.ran = nil
	if r := clikittest.Run(app, "config"); r.Code != 0 || strings.Join(j.ran, "|") != "config" {
		t.Fatalf("config [<command>] runs alone: %+v, ran %v", r, j.ran)
	}
	if r := clikittest.Run(app, "login", "--user", "me"); r.Code != 2 || !strings.Contains(r.Stderr, "Usage: brief login --user=USER --password=PASSWORD\n") {
		t.Fatalf("a required and-group needs every member, and says so: %+v", r)
	}
}

// The flag groups help states rather than rejects.
type groupsCLI struct {
	Send    sendCmd    `cmd:"" default:"withargs" help:"Send a message."`
	Connect connectCmd `cmd:"" help:"Connect to a host."`
	Pick    pickCmd    `cmd:"" help:"Pick a direction."`
}

// sendCmd is the default command, so its groups parse at g's level and g's
// page states them; its required group means a bare g does not parse.
type sendCmd struct {
	Message string `arg:"" optional:"" help:"What to send; stdin when omitted."`
	Print   bool   `xor:"report" help:"Print what was sent."`
	Notify  bool   `xor:"report" help:"Notify the recipient instead."`
	To      string `xor:"dest" required:"" help:"Address to send it to."`
	Stdout  bool   `xor:"dest" required:"" help:"Write it to stdout instead."`
	Account string `and:"login" help:"Account to send from."`
	Secret  string `and:"login" help:"That account's password."`
}

// connectCmd has optional groups that overlap, a required flag in two and
// groups, and a flag an and group requires inside an optional xor group.
type connectCmd struct {
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

// pickCmd has flags in several required xor groups, one of which (corner) is
// no member's first, and a required member hidden beside an optional one,
// which Kong does not require, so help states no group for it.
type pickCmd struct {
	North bool `xor:"vertical,corner" required:"" help:"Go north."`
	South bool `xor:"vertical" required:"" help:"Go south."`
	East  bool `xor:"horizontal,corner" required:"" help:"Go east."`
	West  bool `xor:"horizontal" required:"" help:"Go west."`
	Quiet bool `xor:"noise" help:"Say less."`
	Loud  bool `xor:"noise" required:"" hidden:""`
}

func (*sendCmd) Run() error    { return nil }
func (*connectCmd) Run() error { return nil }
func (*pickCmd) Run() error    { return nil }

func TestFlagGroupHelp(t *testing.T) {
	app := clikit.App{Name: "g", Description: "g groups flags.", New: func() any { return &groupsCLI{} }}
	clikittest.Golden(t, app, "testdata/groups")

	for _, c := range []struct {
		args  []string
		code  int
		names string
	}{
		{[]string{"connect", "--host=h", "--path=p", "--mirror=m", "--into=d"}, 2, "--port"},
		{[]string{"connect", "--host=h", "--path=p", "--port=1", "--mirror=m", "--into=d"}, 0, ""},
		{[]string{"pick", "--south", "--west"}, 2, "--north"},
		{[]string{"pick", "--north", "--west"}, 0, ""},
		{nil, 2, "--to"},
		{[]string{"--print", "--notify", "--stdout"}, 2, "--print and --notify"},
		{[]string{"--account=me", "--stdout"}, 2, "--secret"},
		{[]string{"--stdout", "hi"}, 0, ""},
	} {
		if r := clikittest.Run(app, c.args...); r.Code != c.code || !strings.Contains(r.Stderr, c.names) {
			t.Errorf("%v: Kong does not enforce what help states: %+v", c.args, r)
		}
	}
}

type noteCLI struct {
	Note   noteCmd   `cmd:"" help:"Append a note."`
	Pickup pickupCmd `cmd:"" help:"Pick up a brief."`
	Wait   waitNCmd  `cmd:"" help:"Wait for some jobs."`
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

func (c *noteCmd) Run(ctx *kong.Context) error {
	fmt.Fprintln(ctx.Stdout, strings.Join(c.Text, " "))
	return nil
}

func (*pickupCmd) Run() error { return nil }
func (*waitNCmd) Run() error  { return nil }

var jot = clikit.App{Name: "jot", Description: "jot keeps notes.", New: func() any { return &noteCLI{} }}

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
		r := clikittest.Run(jot, c.args...)
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
	if r := clikittest.Run(jot, "note", "--", "-n", "now"); r.Code != 0 || r.Stdout != "-n now\n" {
		t.Errorf("text after --: %+v", r)
	}
}

// boardPlain is boardCLI without its defaults: Kong's own report of a word
// that names no command, which clikit gives the same way where a default
// that takes no argument was handed the word.
type boardPlain struct {
	Board    boardCmd         `cmd:"" aliases:"b" help:"The account board."`
	Launch   launchCmd        `cmd:"" help:"Launch a session."`
	Latch    launchCmd        `cmd:"" help:"Hold a session's account."`
	Lurch    launchCmd        `cmd:"" hidden:"" help:"Internal: launch anywhere."`
	Cafe     launchCmd        `cmd:"" name:"café" help:"Take a break."`
	Accounts plainAccountsCmd `cmd:"" help:"Manage accounts."`
}

type plainAccountsCmd struct {
	Board  accountsBoardCmd `cmd:"" help:"The board of accounts alone."`
	Remove removeCmd        `cmd:"" help:"Forget an account."`
}

// A word no command matches, which Kong hands to a default that takes no
// argument, is reported as Kong reports it at a level without a default: the
// same line, under the parent's synopsis. Named, the default keeps its own.
func TestWordNoCommandMatches(t *testing.T) {
	// The trace this rests on: Kong selects the default for the word and the
	// default rejects it in these words.
	k, err := kong.New(&boardCLI{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = k.Parse([]string{"lauch"})
	if pe, ok := errors.AsType[*kong.ParseError](err); !ok || pe.Unwrap() != pe.Context.Error || pe.Error() != "unexpected argument lauch" || pe.Context.Selected().Name != "board" {
		t.Fatalf("Kong's trace of a word no command matches: %v", err)
	}

	app, _ := newBoardApp()
	plain := clikit.App{Name: "tool", Description: "tool shows accounts and launches sessions.", New: func() any { return &boardPlain{} }}
	for _, c := range []struct {
		args    []string
		command []string // the command the error concerns
		word    string
	}{
		{[]string{"lauch"}, nil, "lauch"}, // two commands near, one hidden left out
		{[]string{"zzz"}, nil, "zzz"},     // none
		{[]string{"boar"}, nil, "boar"},   // a prefix
		{[]string{"lurc"}, nil, "lurc"},   // a prefix of a hidden command only
		{[]string{"cabx"}, nil, "cabx"},   // two runes from café, three bytes
		{[]string{"--compact", "lauch"}, nil, "lauch"},
		{[]string{"lauch", "--compact"}, nil, "lauch"},
		{[]string{"--", "lauch"}, nil, "lauch"},
		{[]string{"help", "lauch"}, nil, "lauch"},
		{[]string{"accounts", "remov"}, []string{"accounts"}, "remov"},
		{[]string{"--compact", "accounts", "remov"}, []string{"accounts"}, "remov"},
	} {
		r := clikittest.Run(app, c.args...)
		want := clikittest.Run(plain, append(slices.Clone(c.command), c.word)...)
		got, wantLine := strings.Split(r.Stderr, "\n"), strings.Split(want.Stderr, "\n")[0]
		if r.Code != 2 || len(got) != 4 || got[0] != wantLine || !strings.HasPrefix(wantLine, "tool: error: unexpected argument "+c.word) {
			t.Errorf("%v: exit %d, stderr:\n%s\nwant first line %q", c.args, r.Code, r.Stderr, wantLine)
			continue
		}
		help := clikittest.Run(app, append(slices.Clone(c.command), "--help")...)
		if synopsis, _, _ := strings.Cut(help.Stdout, "\n"); got[1] != synopsis {
			t.Errorf("%v: %q is not the synopsis of %v, %q", c.args, got[1], c.command, synopsis)
		}
	}
	if r := clikittest.Run(app, "lauch"); !strings.Contains(r.Stderr, `did you mean one of "launch", "latch"?`) {
		t.Errorf("lauch: %q", r.Stderr)
	}

	// Named, the default is what rejects the word.
	for _, args := range [][]string{{"board", "lauch"}, {"b", "lauch"}, {"board", "--compact", "lauch"}, {"--", "board", "lauch"}} {
		if r := clikittest.Run(app, args...); r.Stderr != "tool: error: unexpected argument lauch\nUsage: tool board [flags]\nRun \"tool board --help\" for details.\n" {
			t.Errorf("%v: %q", args, r.Stderr)
		}
	}
	if r := clikittest.Run(app, "accounts", "board", "remov"); !strings.HasPrefix(r.Stderr, "tool: error: unexpected argument remov\nUsage: tool accounts board [flags]\n") {
		t.Errorf("accounts board remov: %q", r.Stderr)
	}
	// A default that takes a positional takes the word.
	j := &journal{}
	brief := clikit.App{Name: "brief", Description: "brief indexes handoff briefs.", New: func() any { return &grammar{} }, Options: []kong.Option{kong.Bind(j)}}
	if r := clikittest.Run(brief, "lauch"); r.Code != 0 || strings.Join(j.ran, "|") != "show lauchfalse" {
		t.Errorf("brief lauch: %+v, ran %v", r, j.ran)
	}
}
