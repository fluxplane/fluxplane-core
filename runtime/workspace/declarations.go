package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	coreworkspace "github.com/fluxplane/fluxplane-core/core/workspace"
)

// DeclarationLoader loads workspace declarations from a runtime workspace.
type DeclarationLoader struct{}

// NewDeclarationLoader returns a declaration loader.
func NewDeclarationLoader() DeclarationLoader { return DeclarationLoader{} }

// Load reads known workspace declaration paths from ws. Missing declaration
// files are ignored.
func (DeclarationLoader) Load(ctx context.Context, ws Workspace, maxBytes int64) ([]coreworkspace.Workspace, []Warning, error) {
	if ws == nil {
		return nil, nil, nil
	}
	if maxBytes <= 0 {
		maxBytes = 128 * 1024
	}
	paths := []string{".agents/workspaces.json", ".agents/workspace.json"}
	var declarations []coreworkspace.Workspace
	var warnings []Warning
	for _, rel := range paths {
		data, truncated, _, err := ws.ReadFile(ctx, rel, maxBytes)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return declarations, warnings, ctxErr
			}
			warnings = append(warnings, Warning{Code: WarningDeclarationReadFailed, Message: rel + ": " + err.Error()})
			continue
		}
		if truncated {
			warnings = append(warnings, Warning{Code: "declaration_truncated", Message: rel + " exceeded workspace declaration size limit"})
			continue
		}
		loaded, err := ParseDeclarations(data)
		if err != nil {
			warnings = append(warnings, Warning{Code: WarningDeclarationParseFailed, Message: rel + ": " + err.Error()})
			continue
		}
		for _, declaration := range loaded {
			if declaration.Durability == "" {
				declaration.Durability = coreworkspace.DurabilityDurable
			}
			if err := declaration.Validate(); err != nil {
				warnings = append(warnings, Warning{Code: WarningInvalidDeclaration, Message: fmt.Sprintf("%s: workspace declaration %q is invalid: %v", rel, declaration.ID, err)})
				continue
			}
			declarations = append(declarations, declaration)
		}
	}
	return declarations, warnings, nil
}

// ParseDeclarations parses JSON workspace declarations.
func ParseDeclarations(data []byte) ([]coreworkspace.Workspace, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil
	}
	var list []coreworkspace.Workspace
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &list); err != nil {
			return nil, err
		}
		return list, nil
	}
	var doc struct {
		Workspaces []coreworkspace.Workspace `json:"workspaces"`
	}
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
		return nil, err
	}
	return doc.Workspaces, nil
}
