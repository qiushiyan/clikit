// Package clikit is the invocation and help contract shared by the ~/dev Go
// command-line tools. A tool declares its commands as native Kong structs
// (github.com/alecthomas/kong) and describes itself once, as an App; main and
// the tests run that same value.
//
// One declaration drives both parsing and help: a command is a struct whose
// fields are flags and positional arguments, each with a help tag, and whose
// Run method does the work. Long prose belongs in a Help() string method on the
// same type, and titled blocks such as examples in a HelpSections method.
// Nothing about a command's syntax is written twice.
//
//	type CLI struct {
//		Dir string `help:"Directory that holds the store." env:"TOOL_DIR" default:"~/.tool" type:"path"`
//		Add AddCmd `cmd:"" help:"Register a repository."`
//	}
//
//	type AddCmd struct {
//		Path string `arg:"" type:"existingdir" help:"Checkout to register."`
//	}
//
//	func (c *AddCmd) Run(ctx *kong.Context, s *Store) error { ... }
//
//	var app = clikit.App{
//		Name:        "tool",
//		Description: "tool keeps a registry of repositories.",
//		New:         func() any { return &CLI{} },
//		Options: []kong.Option{
//			kong.BindToProvider(func(cli *CLI) (*Store, error) { return openStore(cli.Dir) }),
//		},
//		ExitCodes: map[int]string{3: "a repository is missing"},
//	}
//
//	func main() { os.Exit(app.Run(os.Args[1:])) }
//
// Each invocation parses into a fresh grammar from New, so Options must not
// capture one. A Run method or provider that needs the parsed root or its
// flags takes the root type as a parameter (cli *CLI above); Kong binds it.
//
// Run guarantees:
//
//   - Help always works. --help (or "help [command…]", unless the tool declares
//     a help command of its own) prints the page to stdout and exits 0 before
//     any command runs, any hook after BeforeReset fires, or any environment
//     variable is read. Load configuration and credentials in Run methods or
//     through kong.BindToProvider (or kong.BindSingletonProvider, for a value
//     several providers share), called only when a Run method asks for the
//     value, never in grammar hooks or mappers. No option can take this away.
//   - Help is rendered from the model. The synopsis, arguments, flags, typed
//     placeholders, defaults, enums, environment variables, flag-group
//     constraints, passthrough and exit codes come from the same declaration
//     that parses argv, and Kong's error lines name a flag as help does. A
//     default:"withargs" command's arguments and flags parse at its parent's
//     level, so the parent's page lists them with their constraints; its
//     Help() and sections stay on its own page: a tool whose default command
//     is the whole tool returns the same text and sections from the root's
//     methods. Generated rows wrap at 100 columns; Help() text and
//     sections print verbatim, a command's own sections before the Shared ones
//     it inherits.
//   - A declaration whose help would be missing or untrue fails before parsing:
//     anything visible without help, an empty Description, an enum default
//     outside its values, a branching positional argument, kong.NoDefaultHelp,
//     a command named like a flag ("--", "-x"), which argv cannot reach where
//     help lists it, a default:"withargs" command that is hidden or has
//     commands of its own, or a flag group Kong checks differently from its
//     tags, such as one spanning commands or a required group of one flag.
//     The first test (or run) of the tool names each problem.
//   - Outcomes are classified by origin. Invalid argv exits with App.UsageCode
//     after an error line, the synopsis of the command it concerns and a
//     pointer to its --help, all on stderr. Around a default:"withargs"
//     command, argv is held to its parent's page: a flag of the default typed
//     before another command is refused, since the default would not run, and
//     a word that names no command is reported against the parent, as if it
//     had no default, unless the default takes the word as its argument.
//     A bad value from an environment variable, and anything else that fails,
//     exits with App.FailureCode. A command chooses its own status with Exit
//     (it already reported) or Fail (Run prints the error), each code declared
//     in App.ExitCodes, or reports a usage error it found itself with Usage.
//     UsageCode defaults to 2, which a migrating tool often already uses for
//     something else: set it to the tool's existing code.
//
// Commands write through the bound *kong.Context (ctx.Stdout, ctx.Stderr),
// which are App.Stdout and App.Stderr themselves, and read input from a bound
// io.Reader parameter (App.Stdin); a provider may take the *kong.Context too.
// The clikittest package runs an App with captured output and pins every help
// page. A test swaps a dependency (a clock, tmux, a terminal check) by
// appending an option that binds the same type to a copy of App.Options: Kong
// keeps one binding per type, and the later one wins.
package clikit
