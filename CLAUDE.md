# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

clikit is the invocation and help contract shared by the Go CLIs in `~/dev`: a
tool declares its commands as native Kong structs, describes itself once as a
`clikit.App`, and main and its tests run that same value. Agents learn these
tools from `--help`, so a help page that disagrees with the parser is a wrong
instruction to an agent. Every rule below protects that.

- **The consumer contract:** `doc.go`.
- **How one run flows, and the decisions with the alternatives they beat:**
  `docs/design.md`; read it before proposing a different shape.
- **How a tool uses clikit:** the go-cli skill,
  `~/dotfiles/claude/.claude/skills/go-cli/SKILL.md`.

## Commands

```
make check     # vet, go fix -diff must print nothing (Go 1.27), go test -race
make golden    # CLIKIT_UPDATE=1 go test ./... — rewrite testdata/ after an intended help change; review the diff
go test -run TestGrammarHelp .
```

## Invariants

- **Kong stays visible.** Tools import `kong` for their declarations and pass
  ordinary `kong.Option`s; clikit never re-exports or wraps Kong's grammar. The
  public surface is `App`, `Exit`, `Fail`, `Usage`, `Section`, `HelpSections`
  and `clikittest`'s `Run`, `HelpPages`, `Golden`. Growing it changes what every
  consumer depends on, so a new export earns its place against that.
- **Help ends the run before anything else happens.** Kong's help hook calls
  `Exit(0)` and then returns, so the exit hook panics with `exitSignal` and
  `Run` recovers only that type. Help fires in `BeforeReset`, before
  environment variables, resolvers and later hooks, and `own` in `run.go` is
  applied last and again as a `PostBuild`, so no consumer option can take
  this away.
- **Help is rendered from the model, in `help.go`.** It never delegates to
  `kong.DefaultHelpPrinter`, which misstates xor groups and reflows prose.
  Authored text (`Help()`, sections) prints verbatim. A Kong form the renderer
  cannot state truthfully is rejected in `checkDeclaration` (`check.go`)
  rather than rendered approximately: supporting a new form means rendering it
  correctly or adding it to the rejections.
- **Outcomes are classified by origin.** A `*kong.ParseError` with exit 80
  (Kong's mark for trace and validation errors) is invalid argv and gets the
  usage code with a synopsis; every other parse error, a bad enum from an
  environment variable (`checkEnvEnums`) and a failure to write help get the
  failure code. Commands choose with `Exit`, `Fail` and `Usage`.
- **Kong's wording is restated only where it misleads**, in `usageMessage`
  (`help.go`), each rewrite pinned by `TestUsageMessages`: a Kong release that
  rewords a message fails that test instead of passing it through. Flags are
  named by the placeholder `settlePlaceholders` writes into the model, so
  Kong's own lines and the help page agree.
- **Writers pass through unwrapped.** `ctx.Stdout` is the `App.Stdout` itself,
  so an `*os.File` stays one for terminal detection and nothing shared can
  race; help write errors come back as `helpWriteError` instead. Concurrent
  capture is `clikittest`'s job (its locked buffer).
- **An App is reused across invocations.** `New` builds a fresh grammar per
  run and `Options` must not capture one; providers take the root as a
  parameter.

## Tests and releases

Each rule above has a test in `run_test.go` (`TestOptionsCannotUndoHelp`,
`TestStdoutKeepsItsIdentity`, `TestEnumFromEnvironmentIsAFailure`, …), and the
golden pages under `testdata/` pin rendering for a multi-command tool, a
single-command tool and the rarer grammar forms. A behaviour change lands with
the test that pins it.

The module is public at `github.com/qiushiyan/clikit` and consumed by tag.
Release by tagging `vX.Y.Z` on `main` and pushing the tag; a change to help
output or to the public surface is visible in every consumer's golden pages,
so say so in the tag message. A change to the public surface also updates the
go-cli skill's clikit section in the same pass, since tools are written from
it.

Docs follow `~/dotfiles/docs/documentation-standards.md`, with paths written
from the repository root.
