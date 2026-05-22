package fibermap

// roleGuardName is the sentinel name appended to a route's chain when the
// route has `roles:` in YAML. It is replaced by an actual middleware at Mount
// time (see engine.go).
const roleGuardName = "__role_guard__"

// resolveChain flattens a route's effective middleware chain.
//
//   - sets:       middleware_sets map from YAML.
//   - ancestors:  one slice per ancestor group, ordered outermost-first.
//                 Each entry is a group's combined `middleware_set` + `middleware`
//                 (already concatenated by the caller — see engine.go Mount).
//   - routeMW:    the route's own middleware (set expansion happens here too).
//   - hasRoles:   if true, append roleGuardName as the last element.
//
// Each name that matches a key in `sets` is recursively expanded.
// Duplicates are removed, keeping the first occurrence.
func resolveChain(sets map[string][]string, ancestors [][]string, routeMW []string, hasRoles bool) ([]string, error) {
	seen := map[string]struct{}{}
	out := make([]string, 0, 8)

	add := func(name string) {
		if _, dup := seen[name]; dup {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}

	var expand func(name string)
	expand = func(name string) {
		if children, isSet := sets[name]; isSet {
			for _, c := range children {
				expand(c)
			}
			return
		}
		add(name)
	}

	for _, lvl := range ancestors {
		for _, n := range lvl {
			expand(n)
		}
	}
	for _, n := range routeMW {
		expand(n)
	}
	if hasRoles {
		add(roleGuardName)
	}

	return out, nil
}
