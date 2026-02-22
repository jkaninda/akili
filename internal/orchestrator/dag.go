package orchestrator

import (
	"fmt"

	"github.com/google/uuid"
)

// ValidateDAG checks that the task specifications form a valid DAG:
// no out-of-range indices, no self-references, and no cycles.
func ValidateDAG(specs []TaskSpec) error {
	n := len(specs)
	for i, spec := range specs {
		for _, dep := range spec.DependsOn {
			if dep < 0 || dep >= n {
				return fmt.Errorf("task %d: dependency index %d out of range [0, %d)", i, dep, n)
			}
			if dep == i {
				return fmt.Errorf("task %d: self-dependency", i)
			}
		}
	}

	// Detect cycles using DFS with coloring.
	const (
		white = 0 // Not visited.
		gray  = 1 // In current path.
		black = 2 // Fully processed.
	)
	colors := make([]int, n)

	var dfs func(node int) error
	dfs = func(node int) error {
		colors[node] = gray
		for _, dep := range specs[node].DependsOn {
			switch colors[dep] {
			case gray:
				return fmt.Errorf("cycle detected involving tasks %d and %d", node, dep)
			case white:
				if err := dfs(dep); err != nil {
					return err
				}
			}
		}
		colors[node] = black
		return nil
	}

	for i := range specs {
		if colors[i] == white {
			if err := dfs(i); err != nil {
				return err
			}
		}
	}

	return nil
}

// ResolveDependencies maps TaskSpec indices to actual task UUIDs.
// specs and taskIDs must have the same length.
func ResolveDependencies(specs []TaskSpec, taskIDs []uuid.UUID) map[int][]uuid.UUID {
	deps := make(map[int][]uuid.UUID, len(specs))
	for i, spec := range specs {
		if len(spec.DependsOn) == 0 {
			continue
		}
		resolved := make([]uuid.UUID, len(spec.DependsOn))
		for j, idx := range spec.DependsOn {
			resolved[j] = taskIDs[idx]
		}
		deps[i] = resolved
	}
	return deps
}

// FilterReadyTasks returns tasks from candidates whose dependencies
// are all in the completed set. This is a pure function for testing.
func FilterReadyTasks(candidates []Task, completed map[uuid.UUID]bool) []Task {
	var ready []Task
	for _, t := range candidates {
		allMet := true
		for _, dep := range t.DependsOn {
			if !completed[dep] {
				allMet = false
				break
			}
		}
		if allMet {
			ready = append(ready, t)
		}
	}
	return ready
}
