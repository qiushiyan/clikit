package clikit

import (
	"slices"

	"github.com/alecthomas/kong"
)

// groupKind is how Kong checks a flag group: at most one member of an xor
// group, all or none of an and group.
type groupKind string

const (
	xorGroup groupKind = "xor"
	andGroup groupKind = "and"
)

// keys are the groups of this kind f is tagged with.
func (k groupKind) keys(f *kong.Flag) []string {
	if k == xorGroup {
		return f.Xor
	}
	return f.And
}

// flagGroup is one group among a set of flags, as Kong keys it: by kind and
// name, with its members in declaration order.
type flagGroup struct {
	kind    groupKind
	key     string
	members []*kong.Flag
}

// groupsOf collects the groups flags form, in order of first appearance.
func groupsOf(flags []*kong.Flag) []*flagGroup {
	type id struct {
		kind groupKind
		key  string
	}
	var out []*flagGroup
	index := map[id]*flagGroup{}
	for _, f := range flags {
		for _, kind := range []groupKind{xorGroup, andGroup} {
			for _, key := range kind.keys(f) {
				g := index[id{kind, key}]
				if g == nil {
					g = &flagGroup{kind: kind, key: key}
					index[id{kind, key}] = g
					out = append(out, g)
				}
				g.members = append(g.members, f)
			}
		}
	}
	return out
}

// hasRequiredMember reports whether some member is marked required. Kong then
// demands one member of an xor group and every member of an and group;
// groupProblems (check.go) rejects the shapes where Kong does otherwise.
func (g *flagGroup) hasRequiredMember() bool {
	return slices.ContainsFunc(g.members, func(f *kong.Flag) bool { return f.Required })
}

// tags reports whether f is tagged with g, whichever command declares f.
func (g *flagGroup) tags(f *kong.Flag) bool {
	return slices.Contains(g.kind.keys(f), g.key)
}

// scope is the visible flags that apply to a command, its ancestors' and its
// own, outermost first, with the groups they form.
type scope struct {
	flags  []*kong.Flag
	groups []*flagGroup
}

func scopeOf(n *kong.Node) scope {
	flags := flagsBelow(n, nil)
	return scope{flags, groupsOf(flags)}
}

// requiredXors are the required xor groups f belongs to: Kong demands exactly
// one member of each, so help states the group rather than f alone.
func (s scope) requiredXors(f *kong.Flag) []*flagGroup {
	var out []*flagGroup
	for _, g := range s.groups {
		if g.kind == xorGroup && g.hasRequiredMember() && slices.Contains(g.members, f) {
			out = append(out, g)
		}
	}
	return out
}

// required reports whether Kong demands f itself: it is marked required, or
// it is in a required and group. groupProblems has ensured that a member not
// required itself is in no other and group, so this one step finds every flag
// Kong requires.
func (s scope) required(f *kong.Flag) bool {
	return f.Required || slices.ContainsFunc(s.groups, func(g *flagGroup) bool {
		return g.kind == andGroup && g.hasRequiredMember() && slices.Contains(g.members, f)
	})
}
