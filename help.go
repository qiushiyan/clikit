package clikit

import (
	"fmt"
	"io"
	"maps"
	"reflect"
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

func printHelp(a App) kong.HelpPrinter {
	return func(_ kong.HelpOptions, ctx *kong.Context) error {
		node := ctx.Selected()
		if node == nil {
			node = ctx.Model.Node
		}
		if _, err := io.WriteString(ctx.Stdout, page(a, ctx.Model, node)); err != nil {
			return &helpWriteError{err: err}
		}
		return nil
	}
}

// page renders the full help for node: synopsis, description, arguments,
// flags, constraints, subcommands, declared sections and exit codes.
func page(a App, app *kong.Application, n *kong.Node) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: %s\n", synopsis(app, n))
	if d := n.DefaultCmd; d != nil && d.Tag.Default == "withargs" {
		parts, optional := usageParts(d, n)
		fmt.Fprintf(&b, "   or: %s\n", joinUsage(commandLine(app, n), parts, optional))
	}
	if help := strings.TrimSpace(n.Help); help != "" {
		fmt.Fprintf(&b, "\n%s\n", help)
	}
	if detail := trimBlankLines(n.Detail); detail != "" {
		fmt.Fprintf(&b, "\n%s\n", detail)
	}

	scope := scopeFlags(n)
	var args []row
	for _, p := range n.Positional {
		if !p.Tag.Hidden {
			args = append(args, row{positionalLabel(p), positionalHelp(p, n.Passthrough)})
		}
	}
	var own, inherited []row
	for _, f := range visibleFlags(n) {
		own = append(own, row{flagLabel(f), flagHelp(f, scope)})
	}
	for p := n.Parent; p != nil; p = p.Parent {
		for _, f := range visibleFlags(p) {
			inherited = append(inherited, row{flagLabel(f), flagHelp(f, scope)})
		}
	}
	width := labelWidth(args, own, inherited)
	writeRows(&b, "Arguments", args, width)
	writeRows(&b, "Flags", own, width)
	writeRows(&b, "Inherited flags", inherited, width)
	writeLines(&b, "Constraints", constraints(scope))

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

// synopsis is the one-line usage of n: its command path, required flags (a
// required xor group as alternatives), positional arguments, a command slot
// when it has subcommands, and [flags] when optional flags apply.
func synopsis(app *kong.Application, n *kong.Node) string {
	parts, optional := usageParts(n, nil)
	return joinUsage(commandLine(app, n), parts, optional)
}

func joinUsage(prefix string, parts []string, optional bool) string {
	if optional {
		parts = append(parts, "[flags]")
	}
	return strings.Join(append([]string{prefix}, parts...), " ")
}

// usageParts is what follows n's command path in a usage line. Only flags
// declared below stop count (all of them when stop is nil); groups are judged
// against every flag in n's scope. optional reports flags left out of parts.
func usageParts(n, stop *kong.Node) (parts []string, optional bool) {
	scope := scopeFlags(n)
	seenXor := map[string]bool{}
	for _, f := range flagsBelow(n, stop) {
		if isHelpFlag(f) {
			continue
		}
		switch {
		case len(f.Xor) > 0 && groupRequired(scope, f.Xor[0]):
			key := f.Xor[0]
			if seenXor[key] {
				continue
			}
			seenXor[key] = true
			var alts []string
			for _, m := range groupMembers(scope, key) {
				alts = append(alts, flagUsage(m))
			}
			parts = append(parts, "("+strings.Join(alts, " | ")+")")
		case required(f, scope):
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

// scopeFlags are the visible flags that apply to n: its own and its
// ancestors', outermost first.
func scopeFlags(n *kong.Node) []*kong.Flag {
	return flagsBelow(n, nil)
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

func groupMembers(flags []*kong.Flag, xor string) []*kong.Flag {
	var out []*kong.Flag
	for _, f := range flags {
		if slices.Contains(f.Xor, xor) {
			out = append(out, f)
		}
	}
	return out
}

// groupRequired reports whether Kong demands one member of the xor group.
// checkDeclaration has ensured its members are all required or none is.
func groupRequired(flags []*kong.Flag, xor string) bool {
	return slices.ContainsFunc(groupMembers(flags, xor), func(f *kong.Flag) bool { return f.Required })
}

// andRequired reports whether f belongs to an and group with a required
// member: Kong then requires every member.
func andRequired(f *kong.Flag, scope []*kong.Flag) bool {
	for _, key := range f.And {
		for _, m := range scope {
			if m.Required && slices.Contains(m.And, key) {
				return true
			}
		}
	}
	return false
}

func required(f *kong.Flag, scope []*kong.Flag) bool {
	return f.Required || andRequired(f, scope)
}

// constraints states each flag group in words: Kong enforces at most one
// member of an xor group (exactly one when its members are required) and all
// or none of an and group. A required and group needs no line: every member
// is marked required.
func constraints(flags []*kong.Flag) []string {
	var lines []string
	seen := map[string]bool{}
	for _, f := range flags {
		for _, key := range f.Xor {
			if seen["xor:"+key] {
				continue
			}
			seen["xor:"+key] = true
			members := groupMembers(flags, key)
			if len(members) < 2 {
				continue
			}
			quantity := "At most one of"
			if groupRequired(flags, key) {
				quantity = "Exactly one of"
			}
			lines = append(lines, quantity+" "+flagNames(members)+".")
		}
		for _, key := range f.And {
			if seen["and:"+key] {
				continue
			}
			seen["and:"+key] = true
			var members []*kong.Flag
			for _, m := range flags {
				if slices.Contains(m.And, key) {
					members = append(members, m)
				}
			}
			if len(members) < 2 || andRequired(members[0], flags) {
				continue
			}
			lines = append(lines, "All or none of "+flagNames(members)+".")
		}
	}
	return lines
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
	return "--" + f.Name + "=" + placeholder(f)
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
		name += "=" + placeholder(f)
	}
	for _, alias := range f.Aliases {
		name += ", --" + alias
	}
	return short + name
}

var durationType = reflect.TypeFor[time.Duration]()

// placeholder names a flag's value: the declared placeholder, else its type
// when the name alone would hide it (INT, NUMBER, DURATION), else the name.
// Unlike Kong's, it never shows the default there; the help column does.
func placeholder(f *kong.Flag) string {
	if p, ok := f.Mapper.(kong.PlaceHolderProvider); ok {
		return p.PlaceHolder(f)
	}
	if f.IsMap() {
		return "KEY=VALUE"
	}
	ph := f.PlaceHolder
	if ph == "" {
		t := f.Target.Type()
		if f.IsSlice() {
			t = t.Elem()
		}
		ph = typeWord(t)
	}
	if ph == "" {
		ph = strings.ToUpper(f.Name)
	}
	if f.IsSlice() && f.Tag.Sep != -1 && f.Tag.Type == "" {
		ph += string(f.Tag.Sep) + "..."
	}
	return ph
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
	s := "<" + p.Name + ">"
	if p.IsCumulative() {
		s += " ..."
	}
	if !p.Required {
		s = "[" + s + "]"
	}
	return s
}

func positionalLabel(p *kong.Positional) string {
	s := "<" + p.Name + ">"
	if p.IsCumulative() {
		s += " ..."
	}
	return s
}

func flagHelp(f *kong.Flag, scope []*kong.Flag) string {
	var notes []string
	if len(f.Xor) == 0 && required(f, scope) {
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

// sections collects the Shared sections of n's ancestors, outermost first,
// then all of n's own.
func sections(n *kong.Node) []Section {
	var out []Section
	var chain []*kong.Node
	for p := n.Parent; p != nil; p = p.Parent {
		chain = append(chain, p)
	}
	for _, p := range slices.Backward(chain) {
		for _, s := range declaredSections(p) {
			if s.Shared {
				out = append(out, s)
			}
		}
	}
	return append(out, declaredSections(n)...)
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
		lines := strings.Split(r.help, "\n")
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

func writeLines(b *strings.Builder, title string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s:\n", title)
	for _, l := range lines {
		fmt.Fprintf(b, "  %s\n", l)
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
