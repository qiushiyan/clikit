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
		where := ""
		if n.Type != kong.ApplicationNode {
			where = " of " + strings.TrimPrefix(commandLine(app, n), app.Name+" ")
		}
		if n.Type == kong.ArgumentNode {
			problems = append(problems, "branching argument <"+n.Name+"> is not supported; make it a positional of each command that needs it")
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
					problems = append(problems, fmt.Sprintf("xor group %q marks only some of %s required; Kong then requires none, so mark all or none", key, flagNames(members)))
				}
			}
		}
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
