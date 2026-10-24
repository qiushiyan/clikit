package clikit

import (
	"fmt"
	"io"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alecthomas/kong"
)

// Section is a titled block of help a command declares, printed verbatim
// after the generated parts of its page.
type Section struct {
	Title string
	Body  string

	// Shared prints the section on this command's page and on the page of
	// every command below it, such as a tool-wide overview on the root.
	Shared bool
}

// HelpSections is implemented by a command struct (or the root struct) to add
// sections such as Examples or Output to its help page.
type HelpSections interface {
	HelpSections() []Section
}

// maxLabel is the widest label that keeps its help on the same line; a wider
// one puts its help on the next line instead of pushing every row right.
const maxLabel = 32

// lineWidth is where generated rows wrap: fixed, not the terminal's, so a
// page is the same everywhere and golden files stay stable. Authored text
// (Help(), sections) is never wrapped.
const lineWidth = 100

func printHelp(a App) kong.HelpPrinter {
	return func(_ kong.HelpOptions, ctx *kong.Context) error {
		if _, err := io.WriteString(ctx.Stdout, page(a, ctx.Model, selected(ctx.Model, ctx))); err != nil {
			// %v, not %w: whatever the writer's error wraps, a help page that
			// could not be written is a failure, never a usage error or a status.
			return fmt.Errorf("writing help: %v", err)
		}
		return nil
	}
}

// selected is the command ctx selected, or the root when it selected none or
// parsing failed before there was a context.
func selected(app *kong.Application, ctx *kong.Context) *kong.Node {
	if ctx != nil && ctx.Selected() != nil {
		return ctx.Selected()
	}
	return app.Node
}

// page renders the full help for node: synopsis, description, arguments,
// flags, constraints, subcommands, declared sections and exit codes.
func page(a App, app *kong.Application, n *kong.Node) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: %s\n", synopsis(app, n))
	deflt := defaultWithArgs(n)
	if deflt != nil {
		fmt.Fprintf(&b, "   or: %s\n", usageLine(commandLine(app, n), deflt, n))
	}
	if help := strings.TrimSpace(n.Help); help != "" {
		fmt.Fprintf(&b, "\n%s\n", help)
	}
	if detail := trimBlankLines(n.Detail); detail != "" {
		fmt.Fprintf(&b, "\n%s\n", detail)
	}

	sc := scopeOf(n)
	args := positionalRows(n)
	own := flagRows(visibleFlags(n), sc)
	var inherited []row
	for p := n.Parent; p != nil; p = p.Parent {
		inherited = append(inherited, flagRows(visibleFlags(p), sc)...)
	}
	// A default command's arguments and flags parse at this level too, so
	// this page lists them.
	var defaultRows []row
	if deflt != nil {
		defaultRows = append(positionalRows(deflt), flagRows(visibleFlags(deflt), scopeOf(deflt))...)
	}
	width := labelWidth(args, own, inherited, defaultRows)
	writeRows(&b, "Arguments", args, width)
	writeRows(&b, "Flags", own, width)
	writeRows(&b, "Inherited flags", inherited, width)
	if len(defaultRows) > 0 {
		writeRows(&b, fmt.Sprintf("Arguments and flags of %s, the default command", deflt.Name), defaultRows, width)
	}
	writeRows(&b, "Constraints", constraints(sc), 0)

	var cmds []row
	for _, c := range runnableBelow(n) {
		cmds = append(cmds, row{commandLabel(n, c), commandHelp(c)})
	}
	writeRows(&b, "Commands", cmds, labelWidth(cmds))

	for _, s := range sections(n) {
		body := trimBlankLines(s.Body)
		if body == "" {
			continue
		}
		fmt.Fprintf(&b, "\n%s:\n%s\n", s.Title, indent(body, "  "))
	}
	codes := exitCodes(a)
	writeRows(&b, "Exit codes", codes, labelWidth(codes))

	if len(cmds) > 0 {
		fmt.Fprintf(&b, "\nRun %q for a command's details.\n", commandLine(app, n)+" <command> --help")
	}
	return b.String()
}

// defaultWithArgs is n's default command when its arguments and flags parse
// at n's level (default:"withargs"), or nil.
func defaultWithArgs(n *kong.Node) *kong.Node {
	if d := n.DefaultCmd; d != nil && d.Tag.Default == "withargs" {
		return d
	}
	return nil
}

func positionalRows(n *kong.Node) []row {
	var rows []row
	for _, p := range n.Positional {
		if !p.Tag.Hidden {
			rows = append(rows, row{positionalLabel(p), positionalHelp(p, n.Passthrough)})
		}
	}
	return rows
}

func flagRows(flags []*kong.Flag, sc scope) []row {
	rows := make([]row, len(flags))
	for i, f := range flags {
		rows[i] = row{flagLabel(f), flagHelp(f, sc)}
	}
	return rows
}

// synopsis is the one-line usage of n: its command path, required flags (a
// required xor group as alternatives), positional arguments, a command slot
// when it has subcommands, and [flags] when optional flags apply.
func synopsis(app *kong.Application, n *kong.Node) string {
	return usageLine(commandLine(app, n), n, nil)
}

// usageLine is prefix followed by n's usage parts below stop, and [flags] when
// optional flags apply.
func usageLine(prefix string, n, stop *kong.Node) string {
	parts, optional := usageParts(n, stop)
	if optional {
		parts = append(parts, "[flags]")
	}
	return strings.Join(append([]string{prefix}, parts...), " ")
}

// usageParts is what follows n's command path in a usage line. Only flags
// declared below stop count (all of them when stop is nil); groups are judged
// against every flag in n's scope. optional reports flags left out of parts.
func usageParts(n, stop *kong.Node) (parts []string, optional bool) {
	sc := scopeOf(n)
	seen := map[*flagGroup]bool{}
	for _, f := range flagsBelow(n, stop) {
		if isHelpFlag(f) {
			continue
		}
		xors := sc.requiredXors(f)
		switch {
		case len(xors) > 0:
			for _, g := range xors {
				if seen[g] {
					continue
				}
				seen[g] = true
				alts := make([]string, len(g.members))
				for i, m := range g.members {
					alts[i] = flagUsage(m)
				}
				parts = append(parts, "("+strings.Join(alts, " | ")+")")
			}
		case sc.required(f):
			parts = append(parts, flagUsage(f))
		default:
			optional = true
		}
	}
	for _, p := range n.Positional {
		if !p.Tag.Hidden {
			parts = append(parts, positionalUsage(p))
		}
	}
	if hasVisibleChildren(n) {
		if runnable(n) || n.DefaultCmd != nil {
			parts = append(parts, "[<command>]")
		} else {
			parts = append(parts, "<command>")
		}
	}
	return parts, optional
}

// commandLine is the program name followed by the command path to n.
func commandLine(app *kong.Application, n *kong.Node) string {
	return strings.Join(append([]string{app.Name}, pathBelow(n, nil)...), " ")
}

// pathBelow is the command path from just below stop down to n.
func pathBelow(n, stop *kong.Node) []string {
	var path []string
	for p := n; p != nil && p != stop && p.Type != kong.ApplicationNode; p = p.Parent {
		path = append(path, p.Name)
	}
	slices.Reverse(path)
	return path
}

// flagsBelow are the visible flags of n and its ancestors below stop,
// outermost first.
func flagsBelow(n, stop *kong.Node) []*kong.Flag {
	var chain []*kong.Node
	for p := n; p != nil && p != stop; p = p.Parent {
		chain = append(chain, p)
	}
	var out []*kong.Flag
	for _, p := range slices.Backward(chain) {
		out = append(out, visibleFlags(p)...)
	}
	return out
}

func visibleFlags(n *kong.Node) []*kong.Flag {
	var out []*kong.Flag
	for _, f := range n.Flags {
		if !f.Hidden {
			out = append(out, f)
		}
	}
	return out
}

func isHelpFlag(f *kong.Flag) bool {
	t := f.Target.Type()
	return t.PkgPath() == "github.com/alecthomas/kong" && t.Name() == "helpFlag"
}

// constraints states each flag group in words, one row each. A required and
// group needs no row: every member is marked required.
func constraints(sc scope) []row {
	var rows []row
	for _, g := range sc.groups {
		if len(g.members) < 2 {
			continue
		}
		var quantity string
		switch {
		case g.kind == xorGroup && g.hasRequiredMember():
			quantity = "Exactly one of"
		case g.kind == xorGroup:
			quantity = "At most one of"
		case !g.hasRequiredMember():
			quantity = "All or none of"
		default:
			continue
		}
		rows = append(rows, row{label: quantity + " " + flagNames(g.members) + "."})
	}
	return rows
}

func flagNames(flags []*kong.Flag) string {
	names := make([]string, len(flags))
	for i, f := range flags {
		names[i] = "--" + f.Name
	}
	return strings.Join(names, ", ")
}

func flagUsage(f *kong.Flag) string {
	if f.IsBool() || f.IsCounter() {
		return "--" + f.Name
	}
	return "--" + f.Name + "=" + f.FormatPlaceHolder()
}

func flagLabel(f *kong.Flag) string {
	short := "    "
	if f.Short != 0 {
		short = "-" + string(f.Short) + ", "
	}
	name := "--" + f.Name
	if f.IsBool() {
		switch f.Tag.Negatable {
		case "":
		case "_":
			name = "--[no-]" + f.Name
		default:
			name += ", --" + f.Tag.Negatable
		}
	}
	if !f.IsBool() && !f.IsCounter() {
		name += "=" + f.FormatPlaceHolder()
	}
	for _, alias := range f.Aliases {
		name += ", --" + alias
	}
	return short + name
}

var durationType = reflect.TypeFor[time.Duration]()

// settlePlaceholders writes every value flag's placeholder into the model:
// the declared one, else its type where the name alone would hide it (INT,
// NUMBER, DURATION), else the name. Kong's own messages then name a flag
// exactly as help does ("missing flags: --pickup=PICKUP"), and neither puts
// the default there; the help column states it.
func settlePlaceholders(k *kong.Kong) error {
	var walk func(n *kong.Node)
	walk = func(n *kong.Node) {
		for _, f := range n.Flags {
			if _, ok := f.Mapper.(kong.PlaceHolderProvider); ok || f.PlaceHolder != "" || f.IsMap() || f.IsBool() || f.IsCounter() {
				continue
			}
			t := f.Target.Type()
			if f.IsSlice() {
				t = t.Elem()
			}
			if f.PlaceHolder = typeWord(t); f.PlaceHolder == "" {
				f.PlaceHolder = strings.ToUpper(f.Name)
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(k.Model.Node)
	return nil
}

func typeWord(t reflect.Type) string {
	if t == durationType {
		return "DURATION"
	}
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "INT"
	case reflect.Float32, reflect.Float64:
		return "NUMBER"
	}
	return ""
}

func positionalUsage(p *kong.Positional) string {
	if !p.Required {
		return "[" + positionalLabel(p) + "]"
	}
	return positionalLabel(p)
}

func positionalLabel(p *kong.Positional) string {
	s := "<" + p.Name + ">"
	if p.IsCumulative() {
		s += " ..."
	}
	return s
}

func flagHelp(f *kong.Flag, sc scope) string {
	var notes []string
	// A required xor group says "Exactly one of" under Constraints instead.
	if sc.required(f) && len(sc.requiredXors(f)) == 0 {
		notes = append(notes, "required")
	}
	notes = append(notes, valueNotes(f.Value)...)
	if f.IsCumulative() || f.IsCounter() {
		notes = append(notes, "repeatable")
	}
	return annotate(strings.TrimSpace(f.Help), notes)
}

func positionalHelp(p *kong.Positional, commandPassthrough bool) string {
	var notes []string
	if !p.Required {
		notes = append(notes, "optional")
	}
	notes = append(notes, valueNotes(p)...)
	switch {
	case p.PassthroughMode == kong.PassThroughModePartial:
		notes = append(notes, "flags are parsed up to its first value; everything after that is passed on unparsed")
	case p.PassthroughMode == kong.PassThroughModeAll || commandPassthrough:
		notes = append(notes, "from its first value on, everything is passed on unparsed, flags and -- included")
	}
	return annotate(strings.TrimSpace(p.Help), notes)
}

// valueNotes is what the declaration adds and the help does not already
// interpolate: default, allowed values and environment variables.
func valueNotes(v *kong.Value) []string {
	var notes []string
	if v.HasDefault && v.Default != "" && !kong.HasInterpolatedVar(v.OrigHelp, "default") {
		notes = append(notes, "default: "+v.Default)
	}
	if v.Enum != "" && !kong.HasInterpolatedVar(v.OrigHelp, "enum") {
		notes = append(notes, "one of: "+strings.Join(v.EnumSlice(), ", "))
	}
	if len(v.Tag.Envs) > 0 && !kong.HasInterpolatedVar(v.OrigHelp, "env") {
		envs := make([]string, len(v.Tag.Envs))
		for i, e := range v.Tag.Envs {
			envs[i] = "$" + e
		}
		notes = append(notes, "env: "+strings.Join(envs, ", "))
	}
	return notes
}

// annotate appends notes in parentheses, inside a trailing full stop.
func annotate(help string, notes []string) string {
	if len(notes) == 0 {
		return help
	}
	suffix := "(" + strings.Join(notes, "; ") + ")"
	if before, ok := strings.CutSuffix(help, "."); ok {
		return before + " " + suffix + "."
	}
	if help == "" {
		return suffix
	}
	return help + " " + suffix
}

// runnableBelow lists every visible command under n that can be invoked: the
// leaves, and inner commands with a Run method of their own.
func runnableBelow(n *kong.Node) []*kong.Node {
	var out []*kong.Node
	for _, c := range n.Children {
		if c.Hidden {
			continue
		}
		if c.Leaf() || runnable(c) {
			out = append(out, c)
		}
		out = append(out, runnableBelow(c)...)
	}
	return out
}

func hasVisibleChildren(n *kong.Node) bool {
	return slices.ContainsFunc(n.Children, func(c *kong.Node) bool { return !c.Hidden })
}

func runnable(n *kong.Node) bool {
	t := n.Target
	if !t.IsValid() {
		return false
	}
	if t.MethodByName("Run").IsValid() {
		return true
	}
	return t.CanAddr() && t.Addr().MethodByName("Run").IsValid()
}

// commandLabel is c's path below n followed by what its usage line demands:
// required flags (those of n and above are on n's own line) and positionals.
func commandLabel(n, c *kong.Node) string {
	parts, _ := usageParts(c, n)
	return strings.Join(append(pathBelow(c, n), parts...), " ")
}

func commandHelp(c *kong.Node) string {
	var notes []string
	if c.Parent != nil && c.Parent.DefaultCmd == c {
		notes = append(notes, "default")
	}
	if len(c.Aliases) > 0 {
		notes = append(notes, "alias: "+strings.Join(c.Aliases, ", "))
	}
	return annotate(strings.TrimSpace(c.Help), notes)
}

// sections collects n's own sections, then the Shared sections of its
// ancestors, nearest first: what is specific to the command reads before what
// holds for the whole tool.
func sections(n *kong.Node) []Section {
	out := declaredSections(n)
	for p := n.Parent; p != nil; p = p.Parent {
		for _, s := range declaredSections(p) {
			if s.Shared {
				out = append(out, s)
			}
		}
	}
	return out
}

func declaredSections(n *kong.Node) []Section {
	t := n.Target
	if !t.IsValid() {
		return nil
	}
	if t.CanAddr() {
		if hs, ok := t.Addr().Interface().(HelpSections); ok {
			return hs.HelpSections()
		}
	}
	if hs, ok := t.Interface().(HelpSections); ok {
		return hs.HelpSections()
	}
	return nil
}

// exitCodes lists every status the tool ends with: success, the usage and
// failure codes, and the tool's own, each overridable by App.ExitCodes.
func exitCodes(a App) []row {
	codes := map[int]string{
		0:             "success",
		a.UsageCode:   "invalid arguments; nothing was run",
		a.FailureCode: "failed",
	}
	if a.UsageCode == a.FailureCode {
		codes[a.FailureCode] = "failed, or invalid arguments (then nothing was run)"
	}
	maps.Copy(codes, a.ExitCodes)
	var rows []row
	for _, code := range slices.Sorted(maps.Keys(codes)) {
		rows = append(rows, row{strconv.Itoa(code), codes[code]})
	}
	return rows
}

type row struct{ label, help string }

// labelWidth is the column the help text starts at, shared by the given
// sections so they line up with each other.
func labelWidth(sections ...[]row) int {
	width := 0
	for _, rows := range sections {
		for _, r := range rows {
			if w := utf8.RuneCountInString(r.label); w <= maxLabel {
				width = max(width, w)
			}
		}
	}
	return width
}

func writeRows(b *strings.Builder, title string, rows []row, width int) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s:\n", title)
	pad := strings.Repeat(" ", 2+width+4)
	for _, r := range rows {
		w := utf8.RuneCountInString(r.label)
		lines := wrap(r.help, max(lineWidth-len(pad), 40))
		switch {
		case r.help == "":
			fmt.Fprintf(b, "  %s\n", r.label)
			continue
		case w > width:
			fmt.Fprintf(b, "  %s\n%s%s\n", r.label, pad, lines[0])
		default:
			fmt.Fprintf(b, "  %s%s    %s\n", r.label, strings.Repeat(" ", width-w), lines[0])
		}
		for _, l := range lines[1:] {
			fmt.Fprintf(b, "%s%s\n", pad, strings.TrimSpace(l))
		}
	}
}

func trimBlankLines(s string) string {
	lines := strings.Split(strings.TrimRight(s, " \t\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	return strings.Join(lines, "\n")
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}

// wrap breaks text at spaces so no line passes width runes, keeping its own
// line breaks; a word longer than width stays whole.
func wrap(text string, width int) []string {
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		cur := ""
		for word := range strings.FieldsSeq(line) {
			switch {
			case cur == "":
				cur = word
			case utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(word) > width:
				out = append(out, cur)
				cur = word
			default:
				cur += " " + word
			}
		}
		out = append(out, cur)
	}
	return out
}

// printUsageError writes the error, the synopsis of the command it concerns
// and where to read more, so a caller can correct argv without a second run.
func printUsageError(w io.Writer, app *kong.Application, ctx *kong.Context, err error) {
	node := selected(app, ctx)
	msg, hint := usageMessage(err.Error(), node)
	fmt.Fprintf(w, "%s: error: %s\n", app.Name, msg)
	if hint != "" {
		fmt.Fprintln(w, hint)
	}
	fmt.Fprintf(w, "Usage: %s\n", synopsis(app, node))
	fmt.Fprintf(w, "Run %q for details.\n", commandLine(app, node)+" --help")
}

var (
	// Kong's message for a flag given no value at the end of argv.
	missingValue = regexp.MustCompile(`expected \S+ value but got "EOL" \(<EOL>\)`)
	// Kong brackets an optional positional's name in a decoding error.
	bracketed = regexp.MustCompile(`^\[(<[^>]+>(?: \.\.\.)?)\]: `)
	// Kong offers the help flag for any short flag it does not know.
	helpGuess = regexp.MustCompile(`, did you mean "(?:-h|--help)"\?$`)
)

// usageMessage restates the Kong messages that mislead a caller, and adds a
// hint where the command takes free text: a word starting with "-" reads as a
// flag unless it comes after --. Each rewrite is pinned by a test, so a Kong
// release that changes the wording fails it rather than passing through.
func usageMessage(msg string, n *kong.Node) (string, string) {
	msg = missingValue.ReplaceAllString(msg, "missing value")
	msg = bracketed.ReplaceAllString(msg, "$1: ")
	msg = helpGuess.ReplaceAllString(msg, "")
	hint := ""
	if strings.HasPrefix(msg, "unknown flag ") && slices.ContainsFunc(n.Positional, func(p *kong.Positional) bool {
		return !p.Tag.Hidden && p.IsCumulative() && p.PassthroughMode == kong.PassThroughModeNone
	}) {
		hint = `Text that starts with "-" goes after --, which ends the flags.`
	}
	return msg, hint
}
