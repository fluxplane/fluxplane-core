package workspace

import (
	"fmt"
	"strings"
)

// ID identifies one workspace boundary.
type ID string

// RootKind classifies a workspace root.
type RootKind string

const (
	RootLocal       RootKind = "local"
	RootGitWorktree RootKind = "git_worktree"
	RootVirtual     RootKind = "virtual"
	RootRemote      RootKind = "remote"
)

// OriginKind classifies an origin or alias locator.
type OriginKind string

const (
	OriginConfigured OriginKind = "configured"
	OriginLocal      OriginKind = "local"
	OriginGitHub     OriginKind = "github"
	OriginGitLab     OriginKind = "gitlab"
	OriginGit        OriginKind = "git"
)

// Durability classifies whether a workspace identity is suitable for durable
// cross-session state such as memory.
type Durability string

const (
	DurabilityEphemeral Durability = "ephemeral"
	DurabilityDurable   Durability = "durable"
)

// Workspace describes a selected or declared working boundary.
//
// A workspace may have one root, many roots, parent/member workspace
// relationships, and multiple origins. It is distinct from core/project.Project,
// which describes detected inventory units inside workspace roots.
type Workspace struct {
	ID          ID                `json:"id"`
	Name        string            `json:"name,omitempty"`
	Durability  Durability        `json:"durability,omitempty"`
	Roots       []Root            `json:"roots,omitempty"`
	ParentID    ID                `json:"parent_id,omitempty"`
	Members     []ID              `json:"members,omitempty"`
	Origins     []Origin          `json:"origins,omitempty"`
	Aliases     []Alias           `json:"aliases,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Root is one local, remote, or virtual root included in a workspace.
type Root struct {
	Path       string   `json:"path,omitempty"`
	Kind       RootKind `json:"kind,omitempty"`
	ProjectIDs []string `json:"project_ids,omitempty"`
	Origins    []Origin `json:"origins,omitempty"`
}

// Origin describes evidence for where a workspace or root comes from.
type Origin struct {
	Kind    OriginKind `json:"kind"`
	Locator string     `json:"locator"`
	Subpath string     `json:"subpath,omitempty"`
}

// Alias records another known locator for the same workspace.
type Alias struct {
	Kind    OriginKind `json:"kind"`
	Locator string     `json:"locator"`
	Subpath string     `json:"subpath,omitempty"`
}

// Selection describes the active workspace for a session or tool invocation.
type Selection struct {
	Active    ID   `json:"active"`
	Ancestors []ID `json:"ancestors,omitempty"`
	Members   []ID `json:"members,omitempty"`
}

// Validate checks that the workspace has a stable identity and valid nested
// locators.
func (w Workspace) Validate() error {
	if strings.TrimSpace(string(w.ID)) == "" {
		return fmt.Errorf("workspace: id is empty")
	}
	for i, root := range w.Roots {
		if err := root.Validate(); err != nil {
			return fmt.Errorf("workspace: roots[%d]: %w", i, err)
		}
	}
	for i, origin := range w.Origins {
		if err := origin.Validate(); err != nil {
			return fmt.Errorf("workspace: origins[%d]: %w", i, err)
		}
	}
	for i, alias := range w.Aliases {
		if err := alias.Validate(); err != nil {
			return fmt.Errorf("workspace: aliases[%d]: %w", i, err)
		}
	}
	if hasDuplicateIDs(w.Members) {
		return fmt.Errorf("workspace: duplicate member id")
	}
	return nil
}

// Validate checks that the root has enough information to identify what it
// contains.
func (r Root) Validate() error {
	if strings.TrimSpace(r.Path) == "" && len(r.Origins) == 0 && len(r.ProjectIDs) == 0 {
		return fmt.Errorf("root has no path, origins, or project ids")
	}
	for i, origin := range r.Origins {
		if err := origin.Validate(); err != nil {
			return fmt.Errorf("origins[%d]: %w", i, err)
		}
	}
	if hasDuplicateProjectIDs(r.ProjectIDs) {
		return fmt.Errorf("duplicate project id")
	}
	return nil
}

// Validate checks that the origin has a kind and locator.
func (o Origin) Validate() error {
	if strings.TrimSpace(string(o.Kind)) == "" {
		return fmt.Errorf("origin kind is empty")
	}
	if strings.TrimSpace(o.Locator) == "" {
		return fmt.Errorf("origin locator is empty")
	}
	return nil
}

// Validate checks that the alias has a kind and locator.
func (a Alias) Validate() error {
	if strings.TrimSpace(string(a.Kind)) == "" {
		return fmt.Errorf("alias kind is empty")
	}
	if strings.TrimSpace(a.Locator) == "" {
		return fmt.Errorf("alias locator is empty")
	}
	return nil
}

// Validate checks that the selection has an active workspace and no duplicate
// related workspace IDs.
func (s Selection) Validate() error {
	if strings.TrimSpace(string(s.Active)) == "" {
		return fmt.Errorf("workspace selection: active is empty")
	}
	if hasDuplicateIDs(s.Ancestors) {
		return fmt.Errorf("workspace selection: duplicate ancestor id")
	}
	if hasDuplicateIDs(s.Members) {
		return fmt.Errorf("workspace selection: duplicate member id")
	}
	return nil
}

func hasDuplicateIDs(ids []ID) bool {
	seen := map[ID]bool{}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if seen[id] {
			return true
		}
		seen[id] = true
	}
	return false
}

func hasDuplicateProjectIDs(ids []string) bool {
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if seen[id] {
			return true
		}
		seen[id] = true
	}
	return false
}
