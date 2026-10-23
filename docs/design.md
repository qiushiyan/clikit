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
an `io.Reader`, the environment-enum check, and `own` (writers, exit hook,
help printer), applied both as an option and again after every `PostBuild`.
`checkDeclaration` then rejects any grammar help would describe wrongly, before
a single argument is read.

Kong's `Parse` runs in a fixed order, and clikit's guarantees hang on where
each step sits in it:

1. **Trace** decodes argv. A malformed value or an unknown command fails here
   with Kong's exit 80.
2. **BeforeReset hooks.** The help flag lives here, so `--help` prints and
   ends the run before environment variables, defaults or later hooks are
   touched: a broken config can never block help.
3. **Reset and Resolve** apply environment variables and defaults; a value
   that does not decode fails without exit 80.
4. **BeforeApply.** `checkEnvEnums` reports an environment variable holding a
   value outside its enum, which Kong would otherwise report from validation
   as though argv were wrong.
5. **Validate** checks required flags, groups and enums, with exit 80.

`ctx.Run` then calls the selected command; providers registered with
`kong.BindToProvider` run only when a Run method asks for their value.
`parseFailure` and `commandOutcome` turn the result into a status:

- **Help or `--version`:** 0, on stdout.
- **Exit 80 from Kong, or `Usage(err)`:** `App.UsageCode`, with the error, the
  command's synopsis and a pointer to its `--help`, on stderr.
- **`Exit(n)`:** n, with nothing printed; the command reported it.
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
- **Reject what cannot be rendered truthfully.** An xor group with only some
  members required (Kong then requires none), a branching positional argument,
  an enum default outside its values and `kong.NoDefaultHelp` fail at
  construction. Rendering them approximately would reintroduce the
  disagreement clikit exists to prevent.
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
  instead, and `clikittest` brings the locked buffer tests need.

## Deliberately not built

- **Line wrapping.** Agents read help, and authored text is laid out on
  purpose; a terminal wraps what is too long.
- **Kong `group:` titles.** Presentation only; flags render as one list.
- **A Markdown or JSON export of the command tree.** Skills point agents at
  `--help`, the one consumer that exists. When a skill or README needs an
  export, it is a second renderer over the same model in `help.go`.
- **Shell completion.** Kong has none built in; `jotaen/kong-completion` or
  `miekg/king` can generate it for a tool that wants it.
