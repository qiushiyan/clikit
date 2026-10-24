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
	seenXor := map[string]bool{}
	var walk func(n *kong.Node)
	walk = func(n *kong.Node) {
		where := location(app, n)
		if n.Type == kong.ArgumentNode {
			problems = append(problems, "branching argument <"+n.Name+"> is not supported: make it a positional of each command that needs it")
			return
		}
		if n.Type == kong.CommandNode && !n.Hidden && strings.TrimSpace(n.Help) == "" {
			problems = append(problems, "command "+strings.TrimPrefix(where, " of ")+" has no help")
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
		scope := scopeFlags(n)
		for _, f := range scope {
			for _, key := range f.Xor {
				if seenXor[key] {
					continue
				}
				members := groupMembers(scope, key)
				required := 0
				for _, m := range members {
					if m.Required {
						required++
					}
				}
				if required > 0 && required < len(members) {
					seenXor[key] = true
					problems = append(problems, fmt.Sprintf("xor group %q marks only some of %s required, so Kong requires none: mark all or none", key, flagNames(members)))
				}
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

// group is a flag group as Kong keys it.
type group struct{ kind, key string }

func groupsOf(f *kong.Flag) []group {
	var out []group
	for _, key := range f.Xor {
		out = append(out, group{"xor", key})
	}
	for _, key := range f.And {
		out = append(out, group{"and", key})
	}
	return out
}

// usesGroup reports whether a flag of n, or of a command below it, is in g.
func usesGroup(n *kong.Node, g group) bool {
	for _, f := range n.Flags {
		if slices.Contains(groupsOf(f), g) {
			return true
		}
	}
	return slices.ContainsFunc(n.Children, func(c *kong.Node) bool { return usesGroup(c, g) })
}

// groupProblems reports the groups of n's flags that Kong does not check the
// way help states them. Kong checks a group among one command's flags,
// hidden ones included, so a group spanning commands is never checked; it
// never requires a flag alone in its group; a member of a required xor group
// that is also in an and group makes Kong demand that group whichever member
// is given; and Kong takes a flag's requirement from its last and group.
func groupProblems(app *kong.Application, n *kong.Node) []string {
	var groups []group
	members := map[group][]*kong.Flag{}
	for _, f := range n.Flags {
		for _, g := range groupsOf(f) {
			if members[g] == nil {
				groups = append(groups, g)
			}
			members[g] = append(members[g], f)
		}
	}
	where := location(app, n)
	var problems []string
	reported := map[*kong.Flag]bool{}
	for _, g := range groups {
		flags := members[g]
		var spans []string
		for p := n.Parent; p != nil; p = p.Parent {
			for _, f := range p.Flags {
				if slices.Contains(groupsOf(f), g) {
					spans = append(spans, "--"+f.Name+location(app, p))
				}
			}
		}
		if len(spans) > 0 {
			slices.Reverse(spans)
			for _, f := range flags {
				spans = append(spans, "--"+f.Name+where)
			}
			problems = append(problems, fmt.Sprintf("%s group %q spans %s, but Kong checks a group within one command: declare its flags on one command, or give each command's group its own name", g.kind, g.key, strings.Join(spans, ", ")))
			continue
		}
		// A command below reports a group that spans down to it; a group with
		// no required member is checked as declared.
		if slices.ContainsFunc(n.Children, func(c *kong.Node) bool { return usesGroup(c, g) }) ||
			!slices.ContainsFunc(flags, func(f *kong.Flag) bool { return f.Required }) {
			continue
		}
		if len(flags) == 1 {
			problems = append(problems, fmt.Sprintf("required %s group %q has only --%s%s, and Kong never requires a flag alone in its group: give the group another flag or drop it", g.kind, g.key, flags[0].Name, where))
			continue
		}
		for _, f := range flags {
			i := slices.IndexFunc(groupsOf(f), func(o group) bool { return o.kind == "and" && o != g })
			if i < 0 || reported[f] {
				continue
			}
			and := groupsOf(f)[i].key
			switch {
			case g.kind == "xor":
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
