# clikit

Help pages generated from one declaration, and one shared way to end a run,
for Go command-line tools built on [Kong](https://github.com/alecthomas/kong).

A tool declares its commands as Kong structs and describes itself once as a
`clikit.App`; its `main` and its tests run that same value. clikit renders
every `--help` page from the declaration (synopsis, arguments, flags,
defaults, constraints, exit codes), makes help work before any configuration
loads, rejects declarations whose help would be untrue, and classifies every
outcome into the tool's exit codes. `clikittest` runs the App with captured
output and pins every help page against golden files.

```
go get github.com/qiushiyan/clikit
```

- **The contract and an example:** `doc.go`, also on
  [pkg.go.dev](https://pkg.go.dev/github.com/qiushiyan/clikit).
- **Why it is shaped this way:** `docs/design.md`.

MIT licensed.
