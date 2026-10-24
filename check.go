package clikit

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/alecthomas/kong"
)

// checkDeclaration rejects grammars whose help would be missing or untrue:
// anything visible without help, and the Kong forms clikit cannot render
// faithfully. Help is the only documentation an agent reads, so these are
// errors, not style issues.
func checkDeclaration(k *kong.Kong) error {
	app := k.Model
	var problems []string
	if strings.TrimSpace(app.Help) == "" {
		problems = append(problems, "App.Description is empty")
	}
	if app.HelpFlag == nil {
		problems = append(problems, "kong.NoDefaultHelp removes --help, which every tool must answer")
	}
	var walk func(n *kong.Node)
	walk = func(n *kong.Node) {
		where := location(app, n)
		if n.Type == kong.ArgumentNode {
			problems = append(problems, "branching argument <"+n.Name+"> is not supported: make it a positional of each command that needs it")
			return
		}
		if n.Type == kong.CommandNode && !n.Hidden && strings.TrimSpace(n.Help) == "" {
			problems = append(problems, "command "+strings.Join(pathBelow(n, nil), " ")+" has no help")
		}
		// Kong reads a word that starts with "-" as a flag, or as the end of
		// flags, before it looks for a command; a lone "-" is a word.
		// unmatchedWord (help.go) also counts on no command being called "--".
		if n.Type == kong.CommandNode && !n.Hidden {
			for _, name := range append([]string{n.Name}, n.Aliases...) {
				if len(name) > 1 && strings.HasPrefix(name, "-") {
					problems = append(problems, fmt.Sprintf("command %s is called %q, which argv reads as a flag or as the end of flags, so no caller can type it where help lists it: give it a name without a leading dash", strings.Join(pathBelow(n, nil), " "), name))
				}
			}
		}
		// A withargs default's arguments and flags parse at its parent's level,
		// so the parent's page lists them under the command's name, and an
		// error in them points to the command's own page: hidden, help would
		// name a command its tag hides. Its own commands would parse in the
		// parent's command slot beside the parent's, and Kong never selects
		// their default from there.
		if d := defaultWithArgs(n); d != nil {
			name, parent := strings.Join(pathBelow(d, nil), " "), commandLine(app, n)
			if d.Hidden {
				problems = append(problems, "default command "+name+" is hidden, but its arguments and flags parse as "+parent+"'s own, so help names it all the same: make it visible")
			}
			if hasVisibleChildren(d) {
				problems = append(problems, "default command "+name+" has commands of its own, so "+parent+"'s usage lines cannot tell them from "+parent+"'s commands: make a command without subcommands the default")
			}
		}
		for _, f := range n.Flags {
			if !f.Hidden && strings.TrimSpace(f.Help) == "" {
				problems = append(problems, "flag --"+f.Name+where+" has no help")
			}
		}
		for _, p := range n.Positional {
			if !p.Tag.Hidden && strings.TrimSpace(p.Help) == "" {
				problems = append(problems, "argument <"+p.Name+">"+where+" has no help")
			}
		}
		for _, v := range n.Values() {
			if v.Enum != "" && v.HasDefault && !v.IsCumulative() && !v.EnumMap()[v.Default] {
				problems = append(problems, fmt.Sprintf("%s%s defaults to %q, which is not one of its values", valueName(v), where, v.Default))
			}
		}
		problems = append(problems, groupProblems(app, n)...)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(app.Node)
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(problems, "; "))
}

// location is " of <command path>" for a command, and nothing for the root.
func location(app *kong.Application, n *kong.Node) string {
	if n.Type == kong.ApplicationNode {
		return ""
	}
	return " of " + strings.TrimPrefix(commandLine(app, n), app.Name+" ")
}

// usesGroup reports whether a flag of n, or of a command below it, is in g.
func usesGroup(n *kong.Node, g *flagGroup) bool {
	return slices.ContainsFunc(n.Flags, g.tags) ||
		slices.ContainsFunc(n.Children, func(c *kong.Node) bool { return usesGroup(c, g) })
}

// groupProblems reports the groups of n's flags that Kong does not check the
// way help states them. Kong checks a group among one command's flags,
// hidden ones included, so a group spanning commands is never checked; it
// demands a group only while more than one of its required members is
// missing, so it never requires a flag alone in its group, and an xor group
// with only some members required demands less than its tags say; a member
// of a required xor group that is also in an and group makes Kong demand
// that group whichever member is given; and Kong takes a flag's requirement
// from its last and group.
func groupProblems(app *kong.Application, n *kong.Node) []string {
	where := location(app, n)
	var problems []string
	reported := map[*kong.Flag]bool{}
	for _, g := range groupsOf(n.Flags) {
		var spans []string
		for p := n.Parent; p != nil; p = p.Parent {
			for _, f := range p.Flags {
				if g.tags(f) {
					spans = append(spans, "--"+f.Name+location(app, p))
				}
			}
		}
		if len(spans) > 0 {
			slices.Reverse(spans)
			for _, f := range g.members {
				spans = append(spans, "--"+f.Name+where)
			}
			problems = append(problems, fmt.Sprintf("%s group %q spans %s, but Kong checks a group within one command: declare its flags on one command, or give each command's group its own name", g.kind, g.key, strings.Join(spans, ", ")))
			continue
		}
		// A command below reports a group that spans down to it.
		if slices.ContainsFunc(n.Children, func(c *kong.Node) bool { return usesGroup(c, g) }) {
			continue
		}
		if g.kind == xorGroup {
			// Help states the group by its visible members.
			var shown []*kong.Flag
			required := 0
			for _, f := range g.members {
				if !f.Hidden {
					shown = append(shown, f)
					if f.Required {
						required++
					}
				}
			}
			if required > 0 && required < len(shown) {
				problems = append(problems, fmt.Sprintf("xor group %q marks only some of %s required, so Kong requires less than the tags say: mark all or none", g.key, flagNames(shown)))
				continue
			}
		}
		switch {
		case !g.hasRequiredMember():
			continue // checked as declared
		case len(g.members) == 1:
			problems = append(problems, fmt.Sprintf("required %s group %q has only --%s%s, and Kong never requires a flag alone in its group: give the group another flag or drop it", g.kind, g.key, g.members[0].Name, where))
			continue
		}
		for _, f := range g.members {
			i := slices.IndexFunc(f.And, func(key string) bool { return g.kind != andGroup || key != g.key })
			if i < 0 || reported[f] {
				continue
			}
			and := f.And[i]
			switch {
			case g.kind == xorGroup:
				problems = append(problems, fmt.Sprintf("flag --%s%s is in required xor group %q and in and group %q, so Kong demands all of %q even when another flag of %q is given: keep it to one group", f.Name, where, g.key, and, and, g.key))
			case !f.Required:
				problems = append(problems, fmt.Sprintf("flag --%s%s is in required and group %q and in and group %q without being required itself, so what Kong requires depends on the order of its groups: mark it required or keep it to one and group", f.Name, where, g.key, and))
			default:
				continue
			}
			reported[f] = true
		}
	}
	return problems
}

func valueName(v *kong.Value) string {
	if v.Flag != nil {
		return "flag --" + v.Name
	}
	return "argument <" + v.Name + ">"
}

// checkDefaultFlags refuses a flag Kong parsed for a withargs default that
// does not run. Kong offers a default's flags at its parent's level, so in
// `tool --compact other` it parses --compact as the default's, runs the
// command the later word names, and applies the flag to the default, which
// never runs. The hook runs after help and before resolvers, so the first
// such flag in argv is a usage error before anything is applied.
func checkDefaultFlags(ctx *kong.Context) error {
	ran := selected(ctx.Model, ctx)
	var chain []*kong.Node // the command that runs, up to the root
	for n := ran; n != nil; n = n.Parent {
		chain = append(chain, n)
	}
	for _, p := range ctx.Path {
		if p.Flag == nil {
			continue
		}
		for _, n := range chain {
			d := defaultWithArgs(n)
			if d == nil || slices.Contains(chain, d) || !slices.Contains(d.Flags, p.Flag) {
				continue
			}
			// A command below n may take a flag of that name itself, given
			// after its name. Above n none can: Kong refuses a default's flag
			// that repeats an ancestor's.
			fix := ""
			for _, c := range chain[:slices.Index(chain, n)] {
				if slices.ContainsFunc(c.Flags, func(f *kong.Flag) bool { return f.Name == p.Flag.Name }) {
					name := commandName(ctx.Model, c)
					fix = fmt.Sprintf(": the --%s of %s goes after %s", p.Flag.Name, name, name)
					break
				}
			}
			return Usage(fmt.Errorf("--%s belongs to %s, which %s does not run%s", p.Flag.Name, commandName(ctx.Model, d), commandName(ctx.Model, ran), fix))
		}
	}
	return nil
}

// commandName is n's command path, or the tool's name for the root.
func commandName(app *kong.Application, n *kong.Node) string {
	if n.Type == kong.ApplicationNode {
		return app.Name
	}
	return strings.Join(pathBelow(n, nil), " ")
}

// checkEnvEnums reports an enum value that came from an environment variable
// and is not allowed, before Kong's validation reports it as a mistake in argv.
// A value given in argv wins over the variable and is left to Kong.
func checkEnvEnums(ctx *kong.Context) error {
	fromArgv := map[*kong.Value]bool{}
	for _, p := range ctx.Path {
		switch {
		case p.Flag != nil:
			fromArgv[p.Flag.Value] = true
		case p.Positional != nil:
			fromArgv[p.Positional] = true
		}
	}
	for _, p := range ctx.Path {
		n := p.Node()
		if n == nil {
			continue
		}
		for _, v := range n.Values() {
			if v.Enum == "" || v.IsCumulative() || fromArgv[v] {
				continue
			}
			for _, env := range v.Tag.Envs {
				got, ok := os.LookupEnv(env)
				if !ok {
					continue
				}
				if allowed := v.EnumSlice(); !slices.Contains(allowed, got) {
					return fmt.Errorf("$%s=%q is not a valid %s: must be one of %s", env, got, valueName(v), strings.Join(allowed, ", "))
				}
				break
			}
		}
	}
	return nil
}
