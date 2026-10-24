# clikit design

clikit exists so that the help an agent reads from a `~/dev` Go CLI cannot
disagree with what the tool accepts, and so that every tool ends a run the
same way. This doc is for changing clikit: how one run flows through Kong and
clikit, the decisions that shape it with the alternatives they beat, and what
is deliberately left out. The consumer contract is `doc.go`; the invariants a
change must keep are in `CLAUDE.md`.

## One run

`App.Run` builds a fresh grammar from `New`, then the Kong parser with the
tool's options first and clikit's own last: name, description, stdin bound as
an `io.Reader`, the default-flag and environment-enum checks, and `own`
(writers, exit hook, help printer), applied after every option so a tool's
own `PostBuild` sees the App's writers, and again as the last `PostBuild` so
none can undo it, and `settlePlaceholders`, which writes each flag's value
name into the model.
`checkDeclaration` then rejects any grammar help would describe wrongly, before
a single argument is read. A leading `help` becomes a trailing `--help` unless
the tool declares a `help` command itself.

Kong's `Parse` runs in a fixed order, and clikit's guarantees hang on where
each step sits in it:

1. **Trace** decodes argv. A malformed value or an unknown command fails here
   with Kong's exit 80.
2. **BeforeReset hooks.** The help flag lives here, so `--help` prints and
   ends the run before environment variables, defaults or later hooks are
   touched: a broken config can never block help.
3. **Reset** applies environment variables and defaults; a value that does
   not decode fails without exit 80.
4. **BeforeResolve.** `checkDefaultFlags` refuses a flag Kong parsed for a
   `withargs` default when a later word chose another command, which Kong
   would apply to a default that does not run, as a `Usage` error.
5. **Resolve** applies resolvers, then **BeforeApply** runs `checkEnvEnums`,
   which reports an environment variable holding a value outside its enum
   that Kong would otherwise report from validation as though argv were
   wrong.
6. **Validate** checks required flags, groups and enums, with exit 80.

`ctx.Run` then calls the selected command; providers registered with
`kong.BindToProvider` run only when a Run method asks for their value.
`outcome` turns what `Parse` or the command returned into a status:

- **Help or `--version`:** 0, on stdout.
- **Exit 80 from Kong, or `Usage(err)`:** `App.UsageCode`, with the error
  (restated where Kong's wording misleads), the synopsis of the command it
  concerns and a pointer to its `--help`, on stderr.
- **`Exit(n)`:** n, with nothing printed; the command reported it.
- **`Fail(n, err)`:** n, with one `<tool>: error:` line.
- **Anything else** (a hook, an environment value, a help write, a command
  error): `App.FailureCode`, with one `<tool>: error:` line.

## Decisions, and what they beat

- **Kong, not the standard library or cobra.** The tools' grammars are not
  narrow: flags anywhere, two-level trees, global flags, default commands,
  passthrough to child programs, xor and required groups. A parser of our own
  would own all of that and its edge cases; Kong's parser has years of fixes
  in exactly those places. Cobra gives positional arguments nowhere to carry a
  description and has no enum validation; ff keeps a free-form usage string
  beside its `Exec`, so syntax would still be written twice.
- **A layer that adds to Kong, not one that hides it.** Tools write Kong
  structs and pass Kong options. Re-exporting or wrapping the grammar would
  make clikit a second language for agents to learn and for this repository to
  keep in step with Kong.
- **clikit renders help itself.** `kong.DefaultHelpPrinter` shows a required
  xor pair as if both flags were required, a synopsis the parser rejects, and
  reflows authored prose through `go/doc`. Owning the renderer is also what lets help
  carry generated exit codes, constraints and declared sections.
- **Kong's messages pass through, except where they mislead.** Kong offers
  the help flag for any mistyped short flag, says `got "EOL"` for a missing
  value, and brackets optional positionals; free text starting with a dash
  reads as a flag. `usageMessage` restates those few shapes and adds the `--`
  hint, and `settlePlaceholders` makes Kong's lines name a flag as the usage
  line does. Rewriting every message would mean owning Kong's wording;
  restating only these, each pinned by a test, means a Kong release that
  rewords one fails loudly.
- **A word no command matches is reported at its parent.** Kong hands such a
  word to the parent's `withargs` default, and a default without a
  positional rejects it as its own unexpected argument, as if it had been
  typed. `unmatchedWord` (`help.go`) tells the two apart from the trace:
  Kong records a default it selects for a word without consuming the word,
  and consumes a typed command name first. That is Kong's bookkeeping rather
  than its promise, so `TestWordNoCommandMatches` pins it, and rejecting
  commands named `--` keeps the end-of-flags marker, the one other token
  Kong consumes there, from reading as a typed name. The error then reads as Kong's does where no default takes the word,
  suggestion included; Kong's matcher is unexported, so clikit carries its
  rule and the test holds the two to the same words against the grammar
  without its defaults.
- **A default's flag before another command is refused.** Kong offers a
  `withargs` default's flags at its parent's level and then follows the
  command a later word names, so `tool --json sessions` would run `sessions`
  and apply `--json` to a default that does not run. The parent's page
  states `tool [<command>]` or the default's own form, and that argv is
  neither. Handing the rest of argv to the default instead (`tool --full
  exec` showing the brief "exec") acts on one of two readings, and for a
  default without a positional it only moves the error to the default;
  deciding before Kong parses would mean a tokenizer of clikit's own. When a
  command that runs takes a flag of that name, the error says it goes after
  that command.
- **Generated rows wrap at a fixed 100 columns.** A help tag written as a full
  sentence, with its notes, runs past 150 columns, and unwrapped rows push
  authors to cut tags short. The width is fixed rather than the terminal's so a
  page, and its golden file, is the same everywhere; `Help()` text and
  sections stay exactly as written.
- **A command's own sections come before the Shared ones it inherits.** A
  reader opening one command's page wants that command first; the tool-wide
  text follows.
- **Reject what cannot be rendered truthfully.** A branching positional
  argument, an enum default outside its values, `kong.NoDefaultHelp`, a
  command named like a flag (`--`, `-x`), which Kong reads as a flag before
  it looks for a command, a `withargs` default command that is hidden or has
  commands of its own, and
  a flag group Kong checks differently from its tags fail at construction;
  `check.go` states Kong's rule beside each rejection. Rendering any of these
  approximately would reintroduce the disagreement clikit exists to prevent,
  and modelling Kong's group checks in the renderer would tie help to Kong's
  quirks rather than to the declaration. A hidden `withargs` default could be
  rendered faithfully, as its parent's own invocation with the name left out
  of the page and its errors; it is rejected because no tool needs that, and
  a rejection can be relaxed into a rendering later without moving any
  consumer's pages, where a shipped rendering cannot be withdrawn. Flag
  groups have one model, `groups.go`, which the renderer and `groupProblems`
  both read, and the renderer leans on the rejections: it finds the flags a
  required and group demands one step deep, which is exact only for the
  group shapes that build, so relaxing a group rejection means teaching the
  renderer that shape first.
- **Help ends the run by panicking with `exitSignal`.** Kong's help hook calls
  its exit function and then returns; an exit function that only records the
  status lets parsing continue, so the command would run after its own help.
  `Run` recovers that one type and re-panics anything else.
- **One `App` value for main and tests.** With the grammar, settings and
  options assembled separately in main and in each test, the copies drift (a
  golden test pins a description main does not use), and Kong's usual
  `kong.Bind(&cli.Globals)` carries one run's parsed values into the next.
  `New` per run and options that capture nothing make both impossible.
- **Writers pass through unwrapped.** Recording write errors by wrapping stdout
  races when a command writes from several goroutines and hides an `*os.File`
  from terminal detection. The help printer reports its own write failure
  instead, as an error that wraps nothing, so a writer's error cannot turn it
  into a usage error or a status; `clikittest` brings the locked buffer tests
  need.

## Deliberately not built

- **Wrapping authored text.** `Help()` and sections are laid out on purpose.
- **Shared sections for a subset of commands.** `Shared` reaches every page
  below; a section that serves only some commands is returned by those
  commands' own `HelpSections`, from one shared value.
- **A default command's prose on its parent's page.** Its arguments and
  flags parse at the parent's level, so the parent's page lists them; its
  `Help()` and sections are authored text, and only the author knows whether
  the parent's page should repeat them or already says the same in its own
  words. A tool whose default command is the whole tool returns one shared
  value from both, as above. Rendering the default's prose always would
  double a root page that already describes it, and rendering it only when
  the parent has none would let a one-line root `Help()` silently drop the
  default's examples from the page an agent reads first.
- **Kong `group:` titles.** Presentation only; flags render as one list.
- **A Markdown or JSON export of the command tree.** Skills point agents at
  `--help`, the one consumer that exists. When a skill or README needs an
  export, it is a second renderer over the same model in `help.go`.
- **Shell completion.** Kong has none built in; `jotaen/kong-completion` or
  `miekg/king` can generate it for a tool that wants it.
