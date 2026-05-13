package planexecplugin

import (
	"fmt"
	"sort"
	"strings"
)

func validateSpec(spec PlanSpec) error {
	if strings.TrimSpace(spec.Title) == "" {
		return fmt.Errorf("planexec: title is required")
	}
	if len(spec.Steps) == 0 {
		return fmt.Errorf("planexec: at least one step is required")
	}
	for _, step := range spec.Steps {
		if strings.TrimSpace(step.ID) == "" {
			return fmt.Errorf("planexec: step id is required")
		}
		if strings.TrimSpace(step.Title) == "" {
			return fmt.Errorf("planexec: step %q title is required", step.ID)
		}
	}
	return validateDAG(spec.Steps)
}

func validateDAG(steps []StepSpec) error {
	ids := map[string]bool{}
	for _, step := range steps {
		if ids[step.ID] {
			return fmt.Errorf("planexec: duplicate step %q", step.ID)
		}
		ids[step.ID] = true
	}
	inDegree := map[string]int{}
	edges := map[string][]string{}
	for _, step := range steps {
		inDegree[step.ID] += 0
		for _, dep := range step.DependsOn {
			if dep == step.ID {
				return fmt.Errorf("planexec: step %q depends on itself", step.ID)
			}
			if !ids[dep] {
				return fmt.Errorf("planexec: step %q depends on unknown step %q", step.ID, dep)
			}
			inDegree[step.ID]++
			edges[dep] = append(edges[dep], step.ID)
		}
	}
	var ready []string
	for id, count := range inDegree {
		if count == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	visited := 0
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		visited++
		for _, next := range edges[id] {
			inDegree[next]--
			if inDegree[next] == 0 {
				ready = append(ready, next)
				sort.Strings(ready)
			}
		}
	}
	if visited != len(steps) {
		return fmt.Errorf("planexec: cycle detected")
	}
	return nil
}
