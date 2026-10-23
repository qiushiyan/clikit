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
//   - Help always works. --help prints the page to stdout and exits 0 before
//     any command runs, any hook after BeforeReset fires, or any environment
//     variable is read. Load configuration and credentials in Run methods or
//     through kong.BindToProvider, which is called only when a Run method asks
//     for the value, never in grammar hooks or mappers. No option can take
//     this away.
//   - Help is rendered from the model. The synopsis, arguments, flags, typed
//     placeholders, defaults, enums, environment variables, flag-group
//     constraints, passthrough and exit codes come from the same declaration
//     that parses argv. Help() text and sections are printed verbatim, never
//     reflowed.
//   - A declaration whose help would be missing or untrue fails before parsing:
//     anything visible without help, an empty Description, an enum default
//     outside its values, an xor group with only some members required, a
//     branching positional argument, or kong.NoDefaultHelp. The first test (or
//     run) of the tool names each problem.
//   - Outcomes are classified by origin. Invalid argv exits with App.UsageCode
//     after an error line, the synopsis and a pointer to --help, all on stderr.
//     A bad value from an environment variable, and anything else that fails,
//     exits with App.FailureCode. A command chooses its own status with Exit,
//     declared in App.ExitCodes, or reports a usage error it found itself with
//     Usage.
//
// Commands write through the bound *kong.Context (ctx.Stdout, ctx.Stderr),
// which are App.Stdout and App.Stderr themselves, and read input from a bound
// io.Reader parameter (App.Stdin). The clikittest package runs an App with
// captured output and pins every help page.
package clikit
