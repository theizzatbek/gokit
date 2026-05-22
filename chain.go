package fibermap

import "strings"

// resolveChain flattens a route's effective middleware chain.
//
//   - sets:       middleware_sets map from YAML.
//   - ancestors:  one slice per ancestor group, ordered outermost-first.
//     Each entry is a group's combined `middleware_set` + `middleware`
//     (already concatenated by the caller — see engine.go Mount).
//   - routeMW:    the route's own middleware (set expansion happens here too).
//
// Each ref whose Name matches a key in `sets` is recursively expanded.
// Duplicates are removed using (Name, Args) identity, keeping first occurrence.
func resolveChain(sets map[string][]mwRef, ancestors [][]mwRef, routeMW []mwRef) []mwRef {
	seen := map[string]struct{}{}
	out := make([]mwRef, 0, 8)

	add := func(r mwRef) {
		key := dedupKey(r)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}

	var expand func(r mwRef)
	expand = func(r mwRef) {
		if children, isSet := sets[r.Name]; isSet {
			for _, c := range children {
				expand(c)
			}
			return
		}
		add(r)
	}

	for _, lvl := range ancestors {
		for _, r := range lvl {
			expand(r)
		}
	}
	for _, r := range routeMW {
		expand(r)
	}
	return out
}

func dedupKey(r mwRef) string {
	if r.Args == nil {
		return r.Name
	}
	return r.Name + "\x00" + strings.Join(r.Args, "\x01")
}
