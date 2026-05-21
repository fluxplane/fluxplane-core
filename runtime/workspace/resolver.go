package workspace

import (
	"fmt"
	"path"
	"sort"
	"strings"

	coreworkspace "github.com/fluxplane/engine/core/workspace"
)

// WarningCode classifies a non-fatal workspace resolution issue.
type WarningCode string

const (
	WarningMultipleOriginsNoCanonical WarningCode = "multiple_origins_no_canonical"
	WarningExplicitWorkspaceNotFound  WarningCode = "explicit_workspace_not_found"
	WarningNoEvidence                 WarningCode = "no_evidence"
	WarningDuplicateDeclaration       WarningCode = "duplicate_declaration"
	WarningInvalidDeclaration         WarningCode = "invalid_declaration"
	WarningDeclarationTruncated       WarningCode = "declaration_truncated"
	WarningDeclarationParseFailed     WarningCode = "declaration_parse_failed"
	WarningDeclarationReadFailed      WarningCode = "declaration_read_failed"
)

// Warning records a non-fatal workspace resolution issue.
type Warning struct {
	Code    WarningCode `json:"code,omitempty"`
	Message string      `json:"message"`
}

// Evidence contains IO-free facts gathered by callers or adapters.
type Evidence struct {
	CurrentPath string                 `json:"current_path,omitempty"`
	Root        string                 `json:"root,omitempty"`
	Origins     []coreworkspace.Origin `json:"origins,omitempty"`
}

// ResolveRequest asks Resolver to select the active workspace.
type ResolveRequest struct {
	ExplicitWorkspaceID coreworkspace.ID          `json:"explicit_workspace_id,omitempty"`
	Declarations        []coreworkspace.Workspace `json:"declarations,omitempty"`
	Evidence            Evidence                  `json:"evidence,omitempty"`
}

// ResolveResult is the selected workspace plus relationship metadata.
type ResolveResult struct {
	Active    coreworkspace.Workspace   `json:"active,omitempty"`
	Selection coreworkspace.Selection   `json:"selection,omitempty"`
	Warnings  []Warning                 `json:"warnings,omitempty"`
	Known     []coreworkspace.Workspace `json:"known,omitempty"`
}

// Resolver resolves workspace selections from supplied evidence.
type Resolver struct{}

// NewResolver returns a workspace resolver.
func NewResolver() Resolver { return Resolver{} }

// Resolve selects the active workspace from explicit selection, declarations,
// VCS/local evidence, and parent/member relationships.
func (Resolver) Resolve(req ResolveRequest) (ResolveResult, error) {
	known, normalizeWarnings := normalizeDeclarations(req.Declarations)
	byID := indexByID(known)
	warnings := normalizeWarnings

	if req.ExplicitWorkspaceID != "" {
		if ws, ok := byID[req.ExplicitWorkspaceID]; ok {
			return resultFor(ws, known, warnings), nil
		}
		warnings = append(warnings, Warning{Code: WarningExplicitWorkspaceNotFound, Message: fmt.Sprintf("explicit workspace %q was not declared", req.ExplicitWorkspaceID)})
	}

	if ws, ok := matchDeclaredByPath(known, firstNonEmpty(req.Evidence.CurrentPath, req.Evidence.Root)); ok {
		return resultFor(ws, known, warnings), nil
	}

	if inferred, extraWarnings, ok := inferWorkspace(req.Evidence); ok {
		warnings = append(warnings, extraWarnings...)
		known = append(known, inferred)
		return resultFor(inferred, known, warnings), nil
	}

	warnings = append(warnings, Warning{Code: WarningNoEvidence, Message: "no workspace declaration, root, current path, or origin evidence was provided"})
	return ResolveResult{Warnings: warnings, Known: known}, nil
}

func resultFor(active coreworkspace.Workspace, known []coreworkspace.Workspace, warnings []Warning) ResolveResult {
	ancestors := ancestorsOf(active.ID, known)
	selection := coreworkspace.Selection{Active: active.ID, Ancestors: ancestors, Members: append([]coreworkspace.ID(nil), active.Members...)}
	return ResolveResult{Active: active, Selection: selection, Warnings: warnings, Known: known}
}

func normalizeDeclarations(declarations []coreworkspace.Workspace) ([]coreworkspace.Workspace, []Warning) {
	out := make([]coreworkspace.Workspace, 0, len(declarations))
	var warnings []Warning
	seen := map[coreworkspace.ID]bool{}
	for _, ws := range declarations {
		if ws.ID == "" {
			warnings = append(warnings, Warning{Code: WarningInvalidDeclaration, Message: "workspace declaration has empty id"})
			continue
		}
		if err := ws.Validate(); err != nil {
			warnings = append(warnings, Warning{Code: WarningInvalidDeclaration, Message: fmt.Sprintf("workspace declaration %q is invalid: %v", ws.ID, err)})
			continue
		}
		if seen[ws.ID] {
			warnings = append(warnings, Warning{Code: WarningDuplicateDeclaration, Message: fmt.Sprintf("workspace declaration %q was declared more than once", ws.ID)})
			continue
		}
		seen[ws.ID] = true
		out = append(out, ws)
	}
	return out, warnings
}

func indexByID(workspaces []coreworkspace.Workspace) map[coreworkspace.ID]coreworkspace.Workspace {
	out := map[coreworkspace.ID]coreworkspace.Workspace{}
	for _, ws := range workspaces {
		out[ws.ID] = ws
	}
	return out
}

func matchDeclaredByPath(workspaces []coreworkspace.Workspace, current string) (coreworkspace.Workspace, bool) {
	current = cleanPath(current)
	if current == "" {
		return coreworkspace.Workspace{}, false
	}
	type candidate struct {
		workspace coreworkspace.Workspace
		length    int
	}
	var candidates []candidate
	for _, ws := range workspaces {
		for _, root := range ws.Roots {
			rootPath := cleanPath(root.Path)
			if rootPath == "" {
				continue
			}
			if pathContains(rootPath, current) {
				candidates = append(candidates, candidate{workspace: ws, length: len(rootPath)})
			}
		}
	}
	if len(candidates) == 0 {
		return coreworkspace.Workspace{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].length > candidates[j].length })
	return candidates[0].workspace, true
}

func ancestorsOf(active coreworkspace.ID, workspaces []coreworkspace.Workspace) []coreworkspace.ID {
	byID := indexByID(workspaces)
	var ancestors []coreworkspace.ID
	seen := map[coreworkspace.ID]bool{active: true}
	current := active
	for {
		ws, ok := byID[current]
		if !ok || ws.ParentID == "" || seen[ws.ParentID] {
			break
		}
		ancestors = append(ancestors, ws.ParentID)
		seen[ws.ParentID] = true
		current = ws.ParentID
	}
	return ancestors
}

func inferWorkspace(e Evidence) (coreworkspace.Workspace, []Warning, bool) {
	root := cleanPath(firstNonEmpty(e.Root, e.CurrentPath))
	origins := validOrigins(e.Origins)
	var warnings []Warning
	if len(origins) == 1 && isCanonicalOrigin(origins[0]) {
		origin := origins[0]
		id := workspaceID(origin)
		ws := coreworkspace.Workspace{ID: id, Name: origin.Locator, Durability: workspaceDurability(origin), Roots: []coreworkspace.Root{{Path: root, Kind: rootKind(root, origins), Origins: origins}}, Origins: origins}
		if root != "" {
			ws.Aliases = []coreworkspace.Alias{{Kind: coreworkspace.OriginLocal, Locator: root}}
		}
		return ws, warnings, true
	}
	if len(origins) > 1 {
		warnings = append(warnings, Warning{Code: WarningMultipleOriginsNoCanonical, Message: "multiple workspace origins were provided without an explicit canonical workspace"})
	}
	if root != "" {
		origin := coreworkspace.Origin{Kind: coreworkspace.OriginLocal, Locator: root}
		allOrigins := origins
		if len(allOrigins) == 0 {
			allOrigins = []coreworkspace.Origin{origin}
		}
		ws := coreworkspace.Workspace{ID: workspaceID(origin), Name: path.Base(root), Durability: workspaceDurability(origin), Roots: []coreworkspace.Root{{Path: root, Kind: rootKind(root, allOrigins), Origins: allOrigins}}, Origins: allOrigins}
		return ws, warnings, true
	}
	return coreworkspace.Workspace{}, warnings, false
}

func validOrigins(origins []coreworkspace.Origin) []coreworkspace.Origin {
	out := make([]coreworkspace.Origin, 0, len(origins))
	seen := map[string]bool{}
	for _, origin := range origins {
		if strings.TrimSpace(string(origin.Kind)) == "" || strings.TrimSpace(origin.Locator) == "" {
			continue
		}
		key := string(origin.Kind) + "\x00" + origin.Locator + "\x00" + origin.Subpath
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, origin)
	}
	return out
}

func isCanonicalOrigin(origin coreworkspace.Origin) bool {
	switch origin.Kind {
	case coreworkspace.OriginConfigured, coreworkspace.OriginGitHub, coreworkspace.OriginGitLab, coreworkspace.OriginGit:
		return true
	default:
		return false
	}
}

func workspaceID(origin coreworkspace.Origin) coreworkspace.ID {
	locator := strings.TrimSpace(origin.Locator)
	if origin.Subpath != "" {
		locator += "/" + strings.Trim(strings.TrimSpace(origin.Subpath), "/")
	}
	return coreworkspace.ID("workspace:" + string(origin.Kind) + ":" + locator)
}
func workspaceDurability(origin coreworkspace.Origin) coreworkspace.Durability {
	if isCanonicalOrigin(origin) && origin.Kind != coreworkspace.OriginLocal {
		return coreworkspace.DurabilityDurable
	}
	return coreworkspace.DurabilityEphemeral
}

func rootKind(root string, origins []coreworkspace.Origin) coreworkspace.RootKind {
	for _, origin := range origins {
		switch origin.Kind {
		case coreworkspace.OriginGit, coreworkspace.OriginGitHub, coreworkspace.OriginGitLab:
			return coreworkspace.RootGitWorktree
		case coreworkspace.OriginLocal:
			if root != "" {
				return coreworkspace.RootLocal
			}
		}
	}
	if root != "" {
		return coreworkspace.RootLocal
	}
	return coreworkspace.RootVirtual
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func cleanPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return path.Clean(strings.ReplaceAll(value, "\\", "/"))
}

func pathContains(root, current string) bool {
	root = strings.TrimRight(cleanPath(root), "/")
	current = strings.TrimRight(cleanPath(current), "/")
	if root == "" || current == "" {
		return false
	}
	return current == root || strings.HasPrefix(current, root+"/")
}
