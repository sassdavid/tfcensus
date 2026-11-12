package migrate

import (
	"slices"
)

const (
	actionNoOp   = "no-op"
	actionRead   = "read"
	actionForget = "forget"
)

func only(actions []string, want string) bool { return len(actions) == 1 && actions[0] == want }

// surgeryRejects returns the changes a surgery plan may not have: anything but no-op and read.
// Output changes aren't resource changes, so they never count.
func surgeryRejects(changes []ResourceChange) []ResourceChange {
	var out []ResourceChange
	for _, c := range changes {
		if !only(c.Actions, actionNoOp) && !only(c.Actions, actionRead) {
			out = append(out, c)
		}
	}
	return out
}

// nativeRejects returns the changes a native plan may not have. A destination imports each moved
// address with no changes; a source forgets each one; every other resource is no-op or read.
// Moved addresses the plan doesn't mention are returned with no actions.
func nativeRejects(changes []ResourceChange, role string, moved []string) []ResourceChange {
	var out []ResourceChange
	seen := map[string]bool{}
	for _, c := range changes {
		switch {
		case slices.Contains(moved, c.Address):
			seen[c.Address] = true
			ok := role == RoleDestination && only(c.Actions, actionNoOp) && c.Importing ||
				role == RoleSource && only(c.Actions, actionForget)
			if !ok {
				out = append(out, c)
			}
		case !only(c.Actions, actionNoOp) && !only(c.Actions, actionRead):
			out = append(out, c)
		}
	}
	for _, a := range moved {
		if !seen[a] {
			out = append(out, ResourceChange{Address: a, Actions: []string{}})
		}
	}
	return out
}
