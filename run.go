package clikit

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/alecthomas/kong"
)

// App is one tool's whole command line: main runs it and its tests run the
// same value, so nothing about the tool is assembled twice.
type App struct {
	// Name is the program name in help and error lines. Empty means the base
	// name of os.Args[0].
	Name string

	// Description says what the tool is for; required.
	Description string

	// New returns a fresh grammar, a pointer to the root command struct. Every
	// invocation gets its own, since parsing writes into it.
	New func() any

	// Options are Kong options: kong.Bind, kong.BindToProvider, kong.Vars,
	// mappers and so on. They are shared by every invocation, so they must not
	// capture a grammar instance; a provider or Run method that needs the
	// parsed root takes it as a parameter (func(cli *CLI) (*Store, error)).
	Options []kong.Option

	// ExitCodes adds the statuses the tool's commands return through Exit,
	// with their meaning. 0, UsageCode and FailureCode are always listed; an
	// entry here rewords one of them.
	ExitCodes map[int]string

	// UsageCode is the exit status for invalid argv. Zero means 2.
	UsageCode int

	// FailureCode is the exit status for every other failure. Zero means 1.
	FailureCode int

	// Stdin is bound for Run methods as an io.Reader. Nil means os.Stdin.
	Stdin io.Reader

	// Stdout and Stderr become ctx.Stdout and ctx.Stderr of the bound
	// *kong.Context, unwrapped. Nil means os.Stdout and os.Stderr.
	Stdout, Stderr io.Writer
}

func (a App) withDefaults() App {
	if a.Name == "" {
		a.Name = filepath.Base(os.Args[0])
	}
	if a.UsageCode == 0 {
		a.UsageCode = 2
	}
	if a.FailureCode == 0 {
		a.FailureCode = 1
	}
	if a.Stdin == nil {
		a.Stdin = os.Stdin
	}
	if a.Stdout == nil {
		a.Stdout = os.Stdout
	}
	if a.Stderr == nil {
		a.Stderr = os.Stderr
	}
	return a
}

// kongUsageExit is the status Kong attaches to a ParseError caused by argv
// itself (tracing and validation). Every other ParseError comes from hooks,
// environment variables or defaults.
const kongUsageExit = 80

// Run parses args (without the program name), runs the selected command and
// returns the process exit status. It never calls os.Exit.
func (a App) Run(args []string) (code int) {
	a = a.withDefaults()
	if a.New == nil {
		fmt.Fprintf(a.Stderr, "%s: error: invalid command declaration: App.New is nil\n", a.Name)
		return a.FailureCode
	}

	// clikit's settings go after every option, so a PostBuild sees App's
	// writers, and again as the last PostBuild, so no option can stop help
	// from ending the run or send it elsewhere.
	own := func(k *kong.Kong) error {
		k.Exit = func(code int) { panic(exitSignal(code)) }
		k.Stdout, k.Stderr = a.Stdout, a.Stderr
		return kong.Help(printHelp(a)).Apply(k)
	}
	opts := append(slices.Clone(a.Options),
		kong.Name(a.Name),
		kong.Description(a.Description),
		kong.BindTo(a.Stdin, (*io.Reader)(nil)),
		kong.WithBeforeResolve(checkDefaultFlags),
		kong.WithBeforeApply(checkEnvEnums),
		kong.OptionFunc(own),
		kong.PostBuild(own),
		kong.PostBuild(settlePlaceholders),
	)
	k, err := kong.New(a.New(), opts...)
	if err == nil {
		err = checkDeclaration(k)
	}
	if err != nil {
		fmt.Fprintf(a.Stderr, "%s: error: invalid command declaration: %v\n", a.Name, err)
		return a.FailureCode
	}

	// Kong ends help (and --version) by calling its Exit hook and then
	// returning normally, so the hook panics with exitSignal to stop parsing
	// before any later hook, resolver or command runs.
	defer func() {
		if r := recover(); r != nil {
			sig, ok := r.(exitSignal)
			if !ok {
				panic(r)
			}
			code = int(sig)
		}
	}()

	// `tool help [command…]` is `tool [command…] --help`, unless the tool
	// declares a help command of its own.
	if len(args) > 0 && args[0] == "help" && !hasChild(k.Model.Node, "help") {
		args = append(slices.Clone(args[1:]), "--help")
	}

	ctx, err := k.Parse(args)
	if err == nil {
		err = ctx.Run()
	}
	return a.outcome(k.Model, ctx, err)
}

// outcome turns what parsing or the command returned into the exit status,
// reporting it on stderr unless the command chose Exit. ctx is nil when
// parsing failed.
func (a App) outcome(app *kong.Application, ctx *kong.Context, err error) int {
	if err == nil {
		return 0
	}
	if s, ok := errors.AsType[*statusError](err); ok {
		if s.err != nil {
			fmt.Fprintf(a.Stderr, "%s: error: %v\n", a.Name, s.err)
		}
		return s.code
	}
	if pe, ok := errors.AsType[*kong.ParseError](err); ok {
		// A failed parse returns no context; its error carries one, which a
		// Usage error returned from a hook needs to name its command.
		ctx = pe.Context
		if pe.ExitCode() == kongUsageExit {
			printUsageError(a.Stderr, app, ctx, pe.Unwrap())
			return a.UsageCode
		}
	}
	if u, ok := errors.AsType[*usageError](err); ok {
		printUsageError(a.Stderr, app, ctx, u.err)
		return a.UsageCode
	}
	fmt.Fprintf(a.Stderr, "%s: error: %v\n", a.Name, err)
	return a.FailureCode
}

// Exit makes a command end with status code without printing anything more:
// the command has already reported what happened. Declare the code in
// App.ExitCodes so help lists it. Exit(0) is nil.
func Exit(code int) error {
	if code == 0 {
		return nil
	}
	return &statusError{code: code}
}

// Fail makes a command end with status code after Run prints err the way it
// prints any failure, "<tool>: error: <err>": for a failure the tool gives a
// code of its own, such as an ambiguity its callers tell apart. Declare the
// code in App.ExitCodes so help lists it. Fail(code, nil) is nil, and
// Fail(0, err) is err.
func Fail(code int, err error) error {
	if err == nil || code == 0 {
		return err
	}
	return &statusError{code: code, err: err}
}

// Usage marks err as a usage error the command found itself, such as a
// combination of flags its declaration cannot express. Run prints it with the
// command's synopsis and exits with App.UsageCode. It classifies the outcome
// only: return it before the command has changed anything.
func Usage(err error) error {
	if err == nil {
		return nil
	}
	return &usageError{err: err}
}

// statusError is a status the command chose: with Exit, which leaves err nil
// because the command has reported, or with Fail, whose err Run prints.
type statusError struct {
	code int
	err  error
}

func (e *statusError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit status %d", e.code)
	}
	return e.err.Error()
}

func (e *statusError) Unwrap() error { return e.err }

type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func hasChild(n *kong.Node, name string) bool {
	return slices.ContainsFunc(n.Children, func(c *kong.Node) bool {
		return c.Name == name || slices.Contains(c.Aliases, name)
	})
}

type exitSignal int
