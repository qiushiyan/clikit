package clikit_test

import (
	"strings"
	"testing"

	"github.com/qiushiyan/clikit"
	"github.com/qiushiyan/clikit/clikittest"
)

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

type spanningGroup struct {
	Local bool          `xor:"where" required:"" help:"Use the local copy."`
	Fetch spanningFetch `cmd:"" help:"Fetch a copy."`
}

type spanningFetch struct {
	Remote bool `xor:"where" required:"" help:"Use the remote copy."`
}

// A hidden default is rejected only where its arguments and flags parse at
// its parent's level.
type hiddenDefault struct {
	Show showCmd `cmd:"" default:"withargs" hidden:"" help:"Show a brief."`
	Repo struct {
		List peerCmd `cmd:"" default:"1" hidden:"" help:"List them."`
		Add  peerCmd `cmd:"" help:"Add one."`
	} `cmd:"" help:"Manage repositories."`
}

type branchingDefault struct {
	Repo struct {
		All  bool    `help:"Every machine's."`
		List peerCmd `cmd:"" default:"1" help:"List them."`
		Add  peerCmd `cmd:"" help:"Add one."`
	} `cmd:"" default:"withargs" help:"Manage repositories."`
	Sync peerCmd `cmd:"" help:"Sync them."`
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

// A declaration whose help would be missing or untrue fails before parsing,
// naming each problem by the flag, argument or command it concerns.
func TestDeclarationRejections(t *testing.T) {
	cases := []struct {
		name    string
		app     clikit.App
		want    []string // problems named, by their opening words
		notWant []string
	}{
		{"missing help and untrue tags", clikit.App{Name: "tool", New: func() any { return &badDeclarations{} }}, []string{
			"App.Description is empty",
			"flag --name has no help",
			"command add has no help",
			"argument <path> of add has no help",
			`flag --mode defaults to "c", which is not one of its values`,
			`xor group "how" marks only some of --keep, --delete required`,
		}, []string{"--internal"}},
		{"branching argument", clikit.App{Name: "tool", Description: "tool reads files.", New: func() any { return &branching{} }}, []string{
			"branching argument <file> is not supported",
		}, nil},
		{"group spanning commands", clikit.App{Name: "g", Description: "g groups flags.", New: func() any { return &spanningGroup{} }}, []string{
			`xor group "where" spans --local, --remote of fetch`,
		}, []string{"has only"}},
		{"hidden default command", clikit.App{Name: "brief", Description: "brief indexes handoff briefs.", New: func() any { return &hiddenDefault{} }}, []string{
			"default command show is hidden, but its arguments and flags parse as brief's own",
		}, []string{"repo list"}},
		{"default command with commands", clikit.App{Name: "tool", Description: "tool keeps repositories.", New: func() any { return &branchingDefault{} }}, []string{
			"default command repo has commands of its own",
		}, nil},
		{"groups Kong checks differently", clikit.App{Name: "g", Description: "g groups flags.", New: func() any { return &tangledGroups{} }}, []string{
			`required and group "solo" has only --solo`,
			`flag --keep is in required xor group "how" and in and group "note"`,
			`flag --y is in required and group "a" and in and group "b" without being required itself`,
		}, nil},
	}
	for _, c := range cases {
		r := clikittest.Run(c.app, "--help")
		if r.Code != 1 || r.Stdout != "" {
			t.Errorf("%s: exit %d, stdout %q", c.name, r.Code, r.Stdout)
		}
		for _, w := range c.want {
			if !strings.Contains(r.Stderr, w) {
				t.Errorf("%s: missing %q in %q", c.name, w, r.Stderr)
			}
		}
		for _, w := range c.notWant {
			if strings.Contains(r.Stderr, w) {
				t.Errorf("%s: %q in %q", c.name, w, r.Stderr)
			}
		}
	}
}
