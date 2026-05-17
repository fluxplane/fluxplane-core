package filesystemplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/core/usage"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	Name          = "filesystem"
	DirCreateOp   = "dir_create"
	DirListOp     = "dir_list"
	DirTreeOp     = "dir_tree"
	FileReadOp    = "file_read"
	FileCreateOp  = "file_create"
	FileEditOp    = "file_edit"
	FileDeleteOp  = "file_delete"
	FileStatOp    = "file_stat"
	FileCopyOp    = "file_copy"
	FileMoveOp    = "file_move"
	GlobOp        = "glob"
	GrepOp        = "grep"
	maxReadBytes  = 128 * 1024
	maxWriteBytes = 1024 * 1024
)

// Plugin contributes workspace filesystem operations.
type Plugin struct {
	system system.System
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

// New returns a filesystem plugin using sys.
func New(sys system.System) Plugin { return Plugin{system: sys} }

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Workspace-confined filesystem operations."}
}

// Contributions returns filesystem operation specs.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs := specs()
	return resource.ContributionBundle{
		OperationSets: []operation.Set{{
			Name:        Name,
			Description: "Workspace filesystem operations.",
			Operations:  refs(specs),
		}, {
			Name:        FileEditOp,
			Description: "Existing-file content edit operation.",
			Operations:  []operation.Ref{{Name: FileEditOp}},
		}},
		Operations: specs,
	}, nil
}

// Operations returns executable filesystem operations.
func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	if p.system == nil {
		return nil, fmt.Errorf("filesystemplugin: system is nil")
	}
	ws := p.system.Workspace()
	return []operation.Operation{
		operationruntime.NewTypedResult[dirCreateInput, operation.Rendered](specByName(DirCreateOp), func(ctx operation.Context, req dirCreateInput) operation.Result { return p.dirCreate(ws)(ctx, req) }, operationruntime.WithAccessFields[dirCreateInput](
			operationruntime.PathAccess(func(input dirCreateInput) string { return input.Path }, policy.ActionWorkspaceWrite),
		)),
		operationruntime.NewTypedResult[dirListInput, operation.Rendered](specByName(DirListOp), func(ctx operation.Context, req dirListInput) operation.Result { return p.dirList(ws)(ctx, req) }, operationruntime.WithAccessFields[dirListInput](
			operationruntime.PathAccess(func(input dirListInput) string { return input.Path }, policy.ActionWorkspaceRead),
		)),
		operationruntime.NewTypedResult[dirTreeInput, operation.Rendered](specByName(DirTreeOp), func(ctx operation.Context, req dirTreeInput) operation.Result { return p.dirTree(ws)(ctx, req) }, operationruntime.WithAccessFields[dirTreeInput](
			operationruntime.PathAccess(func(input dirTreeInput) string { return input.Path }, policy.ActionWorkspaceRead),
		)),
		operationruntime.NewTypedResult[fileReadInput, operation.Rendered](specByName(FileReadOp), func(ctx operation.Context, req fileReadInput) operation.Result { return p.fileRead(ws)(ctx, req) }, operationruntime.WithAccessFields[fileReadInput](
			operationruntime.PathAccess(func(input fileReadInput) string { return input.Path }, policy.ActionWorkspaceRead),
		)),
		operationruntime.NewTypedResult[fileCreateInput, operation.Rendered](specByName(FileCreateOp), func(ctx operation.Context, req fileCreateInput) operation.Result { return p.fileCreate(ws)(ctx, req) }, operationruntime.WithAccessFields[fileCreateInput](
			operationruntime.PathAccess(func(input fileCreateInput) string { return input.Path }, policy.ActionWorkspaceWrite),
		)),
		operationruntime.NewTypedResult[fileEditInput, operation.Rendered](specByName(FileEditOp), func(ctx operation.Context, req fileEditInput) operation.Result { return p.fileEdit(ws)(ctx, req) }, operationruntime.WithAccess(fileEditAccess)),
		operationruntime.NewTypedResult[pathInput, operation.Rendered](specByName(FileDeleteOp), func(ctx operation.Context, req pathInput) operation.Result { return p.fileDelete(ws)(ctx, req) }, operationruntime.WithAccessFields[pathInput](
			operationruntime.PathAccess(func(input pathInput) string { return input.Path }, policy.ActionWorkspaceWrite),
		)),
		operationruntime.NewTypedResult[pathInput, operation.Rendered](specByName(FileStatOp), func(ctx operation.Context, req pathInput) operation.Result { return p.fileStat(ws)(ctx, req) }, operationruntime.WithAccessFields[pathInput](
			operationruntime.PathAccess(func(input pathInput) string { return input.Path }, policy.ActionWorkspaceRead),
		)),
		operationruntime.NewTypedResult[copyMoveInput, operation.Rendered](specByName(FileCopyOp), func(ctx operation.Context, req copyMoveInput) operation.Result { return p.fileCopy(ws)(ctx, req) }, operationruntime.WithAccessFields[copyMoveInput](
			operationruntime.PathAccess(func(input copyMoveInput) string { return input.Src }, policy.ActionWorkspaceRead),
			operationruntime.PathAccess(func(input copyMoveInput) string { return input.Dst }, policy.ActionWorkspaceWrite),
		)),
		operationruntime.NewTypedResult[copyMoveInput, operation.Rendered](specByName(FileMoveOp), func(ctx operation.Context, req copyMoveInput) operation.Result { return p.fileMove(ws)(ctx, req) }, operationruntime.WithAccessFields[copyMoveInput](
			operationruntime.PathAccess(func(input copyMoveInput) string { return input.Src }, policy.ActionWorkspaceWrite),
			operationruntime.PathAccess(func(input copyMoveInput) string { return input.Dst }, policy.ActionWorkspaceWrite),
		)),
		operationruntime.NewTypedResult[globInput, operation.Rendered](specByName(GlobOp), func(ctx operation.Context, req globInput) operation.Result { return p.glob(ws)(ctx, req) }, operationruntime.WithAccessFields[globInput](
			operationruntime.PathAccess(func(input globInput) string { return input.Path }, policy.ActionWorkspaceRead, operationruntime.AccessDefault(".")),
		)),
		operationruntime.NewTypedResult[grepInput, operation.Rendered](specByName(GrepOp), func(ctx operation.Context, req grepInput) operation.Result { return p.grep(ws)(ctx, req) }, operationruntime.WithAccessFields[grepInput](
			operationruntime.PathListAccess(func(input grepInput) []string { return input.Paths }, policy.ActionWorkspaceRead, operationruntime.AccessDefault(".")),
		)),
	}, nil
}

func specs() []operation.Spec {
	return []operation.Spec{
		spec[dirCreateInput, operation.Rendered](DirCreateOp, "Create a workspace directory, including parents.", operation.EffectFilesystem, operation.EffectCreate, operation.EffectWriteExternal),
		spec[dirListInput, operation.Rendered](DirListOp, "List a workspace directory.", operation.EffectFilesystem, operation.EffectReadExternal),
		spec[dirTreeInput, operation.Rendered](DirTreeOp, "Render a bounded workspace directory tree.", operation.EffectFilesystem, operation.EffectReadExternal),
		spec[fileReadInput, operation.Rendered](FileReadOp, "Read a bounded workspace file with optional line ranges.", operation.EffectFilesystem, operation.EffectReadExternal),
		spec[fileCreateInput, operation.Rendered](FileCreateOp, "Create or overwrite a workspace file, creating parent directories.", operation.EffectFilesystem, operation.EffectCreate, operation.EffectWriteExternal),
		fileEditSpec(),
		spec[pathInput, operation.Rendered](FileDeleteOp, "Delete one workspace file or empty directory.", operation.EffectFilesystem, operation.EffectDelete, operation.EffectWriteExternal, operation.EffectDestructive),
		spec[pathInput, operation.Rendered](FileStatOp, "Stat one workspace path.", operation.EffectFilesystem, operation.EffectReadExternal),
		spec[copyMoveInput, operation.Rendered](FileCopyOp, "Copy one workspace file to another path.", operation.EffectFilesystem, operation.EffectCreate, operation.EffectWriteExternal),
		spec[copyMoveInput, operation.Rendered](FileMoveOp, "Move one workspace file to another path.", operation.EffectFilesystem, operation.EffectUpdate, operation.EffectWriteExternal),
		spec[globInput, operation.Rendered](GlobOp, "Find workspace paths matching a glob pattern.", operation.EffectFilesystem, operation.EffectReadExternal),
		spec[grepInput, operation.Rendered](GrepOp, "Search workspace files with a regular expression.", operation.EffectFilesystem, operation.EffectReadExternal),
	}
}

func spec[I, O any](name, description string, effects ...operation.Effect) operation.Spec {
	risk := operation.RiskLow
	for _, effect := range effects {
		if effect == operation.EffectDelete || effect == operation.EffectDestructive {
			risk = operation.RiskHigh
			break
		}
		if effect == operation.EffectWriteExternal {
			risk = operation.RiskMedium
		}
	}
	return operationruntime.WithTypedContract[I, O](operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(name)},
		Description: description,
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     effects,
			Risk:        risk,
		},
	})
}

func fileEditSpec() operation.Spec {
	input := operationruntime.WithArrayItems(
		operationruntime.TypeOf[fileEditInput]("file_edit_input"),
		"operations",
		operationruntime.OneOf(
			operationruntime.SchemaFor[editPatchOp](),
			operationruntime.SchemaFor[editLineInsertAfterOp](),
			operationruntime.SchemaFor[editLineInsertBeforeOp](),
			operationruntime.SchemaFor[editRangeReplaceOp](),
			operationruntime.SchemaFor[editRangeDeleteOp](),
			operationruntime.SchemaFor[editAppendOp](),
			operationruntime.SchemaFor[editPrependOp](),
		),
	)
	input.Description = "Existing-file edit request. Use file_create for new files or whole-file creation. All line numbers and exact-text patches refer to the original file; operations are merged, not applied sequentially."
	return operation.Spec{
		Ref:         operation.Ref{Name: FileEditOp},
		Description: "Edit an existing workspace file by merging non-overlapping atomic changes resolved against the original file.",
		Input:       input,
		Output:      operationruntime.TypeOf[operation.Rendered]("file_edit_output"),
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectFilesystem, operation.EffectUpdate, operation.EffectWriteExternal},
			Risk:        operation.RiskMedium,
		},
	}
}

func specByName(name string) operation.Spec {
	for _, spec := range specs() {
		if string(spec.Ref.Name) == name {
			return spec
		}
	}
	return operation.Spec{Ref: operation.Ref{Name: operation.Name(name)}}
}

func refs(specs []operation.Spec) []operation.Ref {
	out := make([]operation.Ref, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec.Ref)
	}
	return out
}

func fileEditAccess(_ operation.Context, input fileEditInput) ([]operationruntime.AccessDescriptor, error) {
	access := []operationruntime.AccessDescriptor{operationruntime.PathDescriptor(input.Path, policy.ActionWorkspaceRead)}
	if !input.DryRun {
		access = append(access, operationruntime.PathDescriptor(input.Path, policy.ActionWorkspaceWrite))
	}
	return access, nil
}

type dirCreateInput struct {
	Path    string `json:"path" jsonschema:"description=Directory path to create.,required"`
	Parents bool   `json:"parents,omitempty" jsonschema:"description=Create parents as needed."`
}

func (p Plugin) dirCreate(ws system.Workspace) operation.Handler {
	return func(ctx operation.Context, input operation.Value) operation.Result {
		var req dirCreateInput
		if err := decode(input, &req); err != nil || strings.TrimSpace(req.Path) == "" {
			return operation.Failed("invalid_dir_create_input", "path is required", nil)
		}
		resolved, err := ws.MkdirAll(ctx, req.Path, 0755)
		if err != nil {
			return operation.Failed("dir_create_failed", err.Error(), nil)
		}
		recordUsage(ctx, FileCreateOp, resolved.Rel, usage.DirectionWrite, 0)
		text := fmt.Sprintf("Created directory: %s", displayPath(resolved))
		return operation.OK(operation.Rendered{Text: text, Data: map[string]any{"path": resolved.Rel}})
	}
}

type dirListInput struct {
	Path       string `json:"path" jsonschema:"description=Directory path to list.,required"`
	ShowHidden bool   `json:"show_hidden,omitempty" jsonschema:"description=Include hidden entries."`
	Pattern    string `json:"pattern,omitempty" jsonschema:"description=Optional glob pattern for entry names."`
}

func (p Plugin) dirList(ws system.Workspace) operation.Handler {
	return func(ctx operation.Context, input operation.Value) operation.Result {
		var req dirListInput
		if err := decode(input, &req); err != nil || strings.TrimSpace(req.Path) == "" {
			return operation.Failed("invalid_dir_list_input", "path is required", nil)
		}
		entries, resolved, err := ws.ReadDir(ctx, req.Path)
		if err != nil {
			return operation.Failed("dir_list_failed", err.Error(), nil)
		}
		rows := make([]map[string]any, 0, len(entries))
		lines := []string{fmt.Sprintf("Directory: %s", displayPath(resolved))}
		for _, entry := range entries {
			if !req.ShowHidden && strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if req.Pattern != "" {
				matched, _ := filepath.Match(req.Pattern, entry.Name())
				if !matched {
					continue
				}
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			kind := "file"
			if entry.IsDir() {
				kind = "dir"
			}
			rows = append(rows, map[string]any{"name": entry.Name(), "kind": kind, "size": info.Size(), "mode": info.Mode().String(), "mod_time": info.ModTime()})
			lines = append(lines, fmt.Sprintf("%s %10d %s", entryType(entry), info.Size(), entry.Name()))
		}
		recordUsage(ctx, DirListOp, resolved.Rel, usage.DirectionRead, float64(len(rows)))
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"path": resolved.Rel, "entries": rows}})
	}
}

type dirTreeInput struct {
	Path       string `json:"path" jsonschema:"description=Root directory path.,required"`
	Depth      int    `json:"depth,omitempty" jsonschema:"description=Maximum recursion depth."`
	ShowHidden bool   `json:"show_hidden,omitempty" jsonschema:"description=Include hidden entries."`
	MaxEntries int    `json:"max_entries,omitempty" jsonschema:"description=Maximum rendered entries."`
}

func (p Plugin) dirTree(ws system.Workspace) operation.Handler {
	return func(ctx operation.Context, input operation.Value) operation.Result {
		var req dirTreeInput
		if err := decode(input, &req); err != nil || strings.TrimSpace(req.Path) == "" {
			return operation.Failed("invalid_dir_tree_input", "path is required", nil)
		}
		depth := req.Depth
		if depth <= 0 {
			depth = 3
		}
		maxEntries := req.MaxEntries
		if maxEntries <= 0 {
			maxEntries = 1000
		}
		walk, root, truncated, err := ws.Walk(ctx, req.Path, system.WalkOptions{Depth: depth, ShowHidden: req.ShowHidden, MaxEntries: maxEntries})
		if err != nil {
			return operation.Failed("dir_tree_failed", err.Error(), nil)
		}
		lines := []string{displayPath(root)}
		entries := make([]string, 0, len(walk))
		for _, entry := range walk {
			label := entry.Name
			if entry.Kind == "dir" {
				label += "/"
			}
			entries = append(entries, entry.Path.Rel)
			lines = append(lines, strings.Repeat("  ", entry.Level-1)+"- "+label)
		}
		recordUsage(ctx, DirTreeOp, root.Rel, usage.DirectionRead, float64(len(entries)))
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"path": root.Rel, "entries": entries, "truncated": truncated}})
	}
}

type fileReadInput struct {
	Path         string `json:"path" jsonschema:"description=File path to read. Globs are not expanded here.,required"`
	StartLine    int    `json:"start_line,omitempty" jsonschema:"description=First 1-indexed line to include."`
	EndLine      int    `json:"end_line,omitempty" jsonschema:"description=Last 1-indexed line to include."`
	LineNumbers  bool   `json:"line_numbers,omitempty" jsonschema:"description=Include line numbers."`
	MaxBytes     int64  `json:"max_bytes,omitempty" jsonschema:"description=Maximum bytes to read."`
	Pattern      string `json:"pattern,omitempty" jsonschema:"description=Regular expression to search for. When set, returns matched regions with surrounding context instead of the full file."`
	ContextLines int    `json:"context_lines,omitempty" jsonschema:"description=Context lines around each match (used with pattern)."`
}

func (p Plugin) fileRead(ws system.Workspace) operation.Handler {
	return func(ctx operation.Context, input operation.Value) operation.Result {
		var req fileReadInput
		if err := decode(input, &req); err != nil || strings.TrimSpace(req.Path) == "" {
			return operation.Failed("invalid_file_read_input", "path is required", nil)
		}
		maxBytes := req.MaxBytes
		if maxBytes <= 0 || maxBytes > maxReadBytes {
			maxBytes = maxReadBytes
		}
		if strings.TrimSpace(req.Pattern) != "" {
			re, err := regexp.Compile(req.Pattern)
			if err != nil {
				return operation.Failed("invalid_file_read_pattern", err.Error(), nil)
			}
			data, _, resolved, err := ws.ReadFile(ctx, req.Path, maxBytes)
			if err != nil {
				return operation.Failed("file_read_failed", err.Error(), nil)
			}
			recordUsage(ctx, FileReadOp, resolved.Rel, usage.DirectionRead, float64(len(data)))
			ctxLines := req.ContextLines
			if ctxLines < 0 {
				ctxLines = 0
			}
			text := renderFilePattern(resolved, string(data), re, ctxLines, req.LineNumbers)
			return operation.OK(operation.Rendered{Text: text, Data: map[string]any{"path": resolved.Rel}})
		}
		if req.StartLine > 0 || req.EndLine > 0 {
			data, firstLine, truncated, resolved, err := ws.ReadFileLines(ctx, req.Path, req.StartLine, req.EndLine, maxBytes)
			if err != nil {
				return operation.Failed("file_read_failed", err.Error(), nil)
			}
			content := string(data)
			text := renderFileRange(resolved, content, firstLine, req.LineNumbers, truncated)
			recordUsage(ctx, FileReadOp, resolved.Rel, usage.DirectionRead, float64(len(data)))
			return operation.OK(operation.Rendered{Text: text, Data: map[string]any{"path": resolved.Rel, "content": content, "truncated": truncated}})
		}
		data, truncated, resolved, err := ws.ReadFile(ctx, req.Path, maxBytes)
		if err != nil {
			return operation.Failed("file_read_failed", err.Error(), nil)
		}
		text := renderFile(resolved, string(data), req.StartLine, req.EndLine, req.LineNumbers, truncated)
		recordUsage(ctx, FileReadOp, resolved.Rel, usage.DirectionRead, float64(len(data)))
		return operation.OK(operation.Rendered{Text: text, Data: map[string]any{"path": resolved.Rel, "content": string(data), "truncated": truncated}})
	}
}

type fileCreateInput struct {
	Path      string `json:"path" jsonschema:"description=File path to write. Parent directories are created.,required"`
	Content   string `json:"content" jsonschema:"description=Complete file content.,required"`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"description=Overwrite if path exists."`
}

func (p Plugin) fileCreate(ws system.Workspace) operation.Handler {
	return func(ctx operation.Context, input operation.Value) operation.Result {
		var req fileCreateInput
		if err := decode(input, &req); err != nil || strings.TrimSpace(req.Path) == "" {
			return operation.Failed("invalid_file_create_input", "path is required", nil)
		}
		if len(req.Content) > maxWriteBytes {
			return operation.Rejected("file_create_too_large", "content exceeds write limit", map[string]any{"max_bytes": maxWriteBytes})
		}
		resolved, err := ws.WriteFile(ctx, req.Path, []byte(req.Content), 0644, req.Overwrite)
		if err != nil {
			return operation.Failed("file_create_failed", err.Error(), nil)
		}
		recordUsage(ctx, FileCreateOp, resolved.Rel, usage.DirectionWrite, float64(len(req.Content)))
		text := fmt.Sprintf("Wrote %d bytes to %s", len(req.Content), displayPath(resolved))
		return operation.OK(operation.Rendered{Text: text, Data: map[string]any{"path": resolved.Rel, "bytes": len(req.Content)}})
	}
}

type fileEditInput struct {
	Path       string            `json:"path" jsonschema:"description=Existing workspace file path to edit. The file must already exist; use file_create for new files or whole-file creation.,required"`
	DryRun     bool              `json:"dry_run,omitempty" jsonschema:"description=When true, validate and render the planned edit without writing the file."`
	DiffMode   string            `json:"diff_mode,omitempty" jsonschema:"description=Controls response diff verbosity only. full (default) returns the final unified diff against the original file; atomic returns one unified diff per atomic operation against the original file; none returns no diff.,enum=full,enum=atomic,enum=none"`
	Operations []json.RawMessage `json:"operations" jsonschema:"description=Ordered atomic edits. All line numbers and exact-text patches refer to the original file. Every operation is resolved against the original file before any changes are merged. Operations must target non-overlapping original ranges; same-boundary inserts are applied in request order.,required,minItems=1"`
}

type editOpHead struct {
	Op string `json:"op"`
}

type editPatchOp struct {
	Op  string  `json:"op" jsonschema:"description=Exact-text replacement operation.,enum=patch,required"`
	Old *string `json:"old" jsonschema:"description=Exact text to find in the original file. The first match is replaced once; empty text is invalid.,required"`
	New *string `json:"new" jsonschema:"description=Replacement text.,required"`
}

type editLineInsertAfterOp struct {
	Op      string  `json:"op" jsonschema:"description=Insert content after a 1-indexed line in the original file.,enum=insert_after,required"`
	Line    int     `json:"line" jsonschema:"description=Original 1-indexed line after which content is inserted.,minimum=1,required"`
	Content *string `json:"content" jsonschema:"description=Content to insert exactly as provided.,required"`
}

type editLineInsertBeforeOp struct {
	Op      string  `json:"op" jsonschema:"description=Insert content before a 1-indexed line in the original file.,enum=insert_before,required"`
	Line    int     `json:"line" jsonschema:"description=Original 1-indexed line before which content is inserted.,minimum=1,required"`
	Content *string `json:"content" jsonschema:"description=Content to insert exactly as provided.,required"`
}

type editRangeReplaceOp struct {
	Op        string  `json:"op" jsonschema:"description=Replace an inclusive original line range.,enum=replace_range,required"`
	StartLine int     `json:"start_line" jsonschema:"description=First original 1-indexed line to replace.,minimum=1,required"`
	EndLine   int     `json:"end_line" jsonschema:"description=Last original 1-indexed line to replace inclusive.,minimum=1,required"`
	Content   *string `json:"content" jsonschema:"description=Replacement content for the whole line range.,required"`
}

type editRangeDeleteOp struct {
	Op        string `json:"op" jsonschema:"description=Delete an inclusive original line range.,enum=delete_range,required"`
	StartLine int    `json:"start_line" jsonschema:"description=First original 1-indexed line to delete.,minimum=1,required"`
	EndLine   int    `json:"end_line" jsonschema:"description=Last original 1-indexed line to delete inclusive.,minimum=1,required"`
}

type editAppendOp struct {
	Op      string  `json:"op" jsonschema:"description=Append content at end of file.,enum=append,required"`
	Content *string `json:"content" jsonschema:"description=Content to append exactly as provided.,required"`
}

type editPrependOp struct {
	Op      string  `json:"op" jsonschema:"description=Prepend content at beginning of file.,enum=prepend,required"`
	Content *string `json:"content" jsonschema:"description=Content to prepend exactly as provided.,required"`
}

type editFragment struct {
	Index     int    `json:"index"`
	Op        string `json:"op"`
	Start     int    `json:"start_byte"`
	End       int    `json:"end_byte"`
	New       string `json:"-"`
	Line      int    `json:"line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	OldText   string `json:"old_text,omitempty"`
	Applied   bool   `json:"applied"`
	Reason    string `json:"reason,omitempty"`
	Diff      string `json:"diff,omitempty"`
	SortOrder int    `json:"-"`
}

func (p Plugin) fileEdit(ws system.Workspace) operation.Handler {
	return func(ctx operation.Context, input operation.Value) operation.Result {
		var req fileEditInput
		if err := decode(input, &req); err != nil || strings.TrimSpace(req.Path) == "" {
			return operation.Failed("invalid_file_edit_input", "path is required", nil)
		}
		if len(req.Operations) == 0 {
			return operation.Failed("invalid_file_edit_input", "at least one operation is required", nil)
		}
		diffMode := strings.TrimSpace(req.DiffMode)
		if diffMode == "" {
			diffMode = "full"
		}
		switch diffMode {
		case "full", "atomic", "none":
		default:
			return operation.Failed("invalid_file_edit_input", "diff_mode must be full, atomic, or none", map[string]any{"diff_mode": req.DiffMode})
		}
		fileData, truncated, resolved, err := ws.ReadFile(ctx, req.Path, maxWriteBytes)
		if err != nil {
			return operation.Failed("file_edit_failed", err.Error(), nil)
		}
		if truncated {
			return operation.Failed("file_edit_too_large", "file exceeds edit read limit", map[string]any{"path": resolved.Rel, "max_bytes": maxWriteBytes})
		}
		before := string(fileData)
		lineIndex := buildLineIndex(before)
		fragments := make([]editFragment, 0, len(req.Operations))
		for i, raw := range req.Operations {
			fragment, err := resolveEditOperation(i, before, lineIndex, raw)
			if err != nil {
				return operation.Failed("invalid_file_edit_operation", err.Error(), map[string]any{"path": resolved.Rel, "index": i})
			}
			fragments = append(fragments, fragment)
		}
		if err := validateEditFragments(fragments); err != nil {
			return operation.Failed("file_edit_overlap", err.Error(), map[string]any{"path": resolved.Rel, "operations": fragments})
		}
		after := mergeEditFragments(before, fragments)
		if len(after) > maxWriteBytes {
			return operation.Rejected("file_edit_too_large", "edited content exceeds write limit", map[string]any{"path": resolved.Rel, "max_bytes": maxWriteBytes})
		}
		fullDiff := unifiedDiff(resolved.Rel, before, after)
		if diffMode == "atomic" {
			for i := range fragments {
				fragments[i].Diff = unifiedDiff(resolved.Rel, before, mergeEditFragments(before, []editFragment{fragments[i]}))
			}
		}
		for i := range fragments {
			fragments[i].Applied = true
		}
		if !req.DryRun {
			if _, err := ws.WriteFile(ctx, req.Path, []byte(after), 0644, true); err != nil {
				return operation.Failed("file_edit_failed", err.Error(), nil)
			}
			recordUsage(ctx, FileEditOp, resolved.Rel, usage.DirectionWrite, float64(len(after)))
		}
		action := "Would edit"
		if !req.DryRun {
			action = "Edited"
		}
		data := map[string]any{"path": resolved.Rel, "dry_run": req.DryRun, "diff_mode": diffMode, "operations": fragments}
		text := fmt.Sprintf("%s %s: %d operation(s) resolved against the original file.", action, displayPath(resolved), len(fragments))
		switch diffMode {
		case "full":
			data["diff"] = fullDiff
			if fullDiff != "" {
				text += "\n\n" + fullDiff
			}
		case "atomic":
			atomicDiffs := make([]string, 0, len(fragments))
			for _, fragment := range fragments {
				atomicDiffs = append(atomicDiffs, fragment.Diff)
			}
			data["atomic_diffs"] = atomicDiffs
			var blocks []string
			for _, fragment := range fragments {
				if fragment.Diff != "" {
					blocks = append(blocks, fmt.Sprintf("# operation %d (%s)\n%s", fragment.Index, fragment.Op, fragment.Diff))
				}
			}
			if len(blocks) > 0 {
				text += "\n\n" + strings.Join(blocks, "\n")
			}
		}
		renderedText := strings.TrimSpace(text)
		return operation.OK(operation.Rendered{Text: renderedText, Model: renderedText, Data: data})
	}
}

type pathInput struct {
	Path string `json:"path" jsonschema:"description=Workspace path.,required"`
}

func (p Plugin) fileDelete(ws system.Workspace) operation.Handler {
	return func(ctx operation.Context, input operation.Value) operation.Result {
		var req pathInput
		if err := decode(input, &req); err != nil || strings.TrimSpace(req.Path) == "" {
			return operation.Failed("invalid_file_delete_input", "path is required", nil)
		}
		resolved, err := ws.Remove(ctx, req.Path)
		if err != nil {
			return operation.Failed("file_delete_failed", err.Error(), nil)
		}
		recordUsage(ctx, FileDeleteOp, resolved.Rel, usage.DirectionWrite, 1)
		text := fmt.Sprintf("Deleted %s", displayPath(resolved))
		return operation.OK(operation.Rendered{Text: text, Data: map[string]any{"path": resolved.Rel}})
	}
}

func (p Plugin) fileStat(ws system.Workspace) operation.Handler {
	return func(ctx operation.Context, input operation.Value) operation.Result {
		var req pathInput
		if err := decode(input, &req); err != nil || strings.TrimSpace(req.Path) == "" {
			return operation.Failed("invalid_file_stat_input", "path is required", nil)
		}
		info, resolved, err := ws.Stat(ctx, req.Path)
		if err != nil {
			return operation.Failed("file_stat_failed", err.Error(), nil)
		}
		data := map[string]any{"path": resolved.Rel, "size": info.Size(), "mode": info.Mode().String(), "mod_time": info.ModTime(), "is_dir": info.IsDir()}
		text := fmt.Sprintf("%s size=%d mode=%s modified=%s dir=%v", displayPath(resolved), info.Size(), info.Mode(), info.ModTime().Format(time.RFC3339), info.IsDir())
		recordUsage(ctx, FileStatOp, resolved.Rel, usage.DirectionRead, 1)
		return operation.OK(operation.Rendered{Text: text, Data: data})
	}
}

type copyMoveInput struct {
	Src       string `json:"src" jsonschema:"description=Source file path.,required"`
	Dst       string `json:"dst" jsonschema:"description=Destination file path.,required"`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"description=Overwrite destination if it exists."`
}

func (p Plugin) fileCopy(ws system.Workspace) operation.Handler {
	return func(ctx operation.Context, input operation.Value) operation.Result {
		var req copyMoveInput
		if err := decode(input, &req); err != nil || req.Src == "" || req.Dst == "" {
			return operation.Failed("invalid_file_copy_input", "src and dst are required", nil)
		}
		src, dst, bytes, err := ws.CopyFile(ctx, req.Src, req.Dst, req.Overwrite)
		if err != nil {
			return operation.Failed("file_copy_failed", err.Error(), nil)
		}
		recordUsage(ctx, FileCopyOp, src.Rel, usage.DirectionRead, float64(bytes))
		recordUsage(ctx, FileCopyOp, dst.Rel, usage.DirectionWrite, float64(bytes))
		text := fmt.Sprintf("Copied %s to %s", displayPath(src), displayPath(dst))
		return operation.OK(operation.Rendered{Text: text, Data: map[string]any{"src": src.Rel, "dst": dst.Rel, "bytes": bytes}})
	}
}

func (p Plugin) fileMove(ws system.Workspace) operation.Handler {
	return func(ctx operation.Context, input operation.Value) operation.Result {
		var req copyMoveInput
		if err := decode(input, &req); err != nil || req.Src == "" || req.Dst == "" {
			return operation.Failed("invalid_file_move_input", "src and dst are required", nil)
		}
		src, dst, bytes, err := ws.MoveFile(ctx, req.Src, req.Dst, req.Overwrite)
		if err != nil {
			return operation.Failed("file_move_failed", err.Error(), nil)
		}
		recordUsage(ctx, FileMoveOp, src.Rel, usage.DirectionWrite, float64(bytes))
		recordUsage(ctx, FileMoveOp, dst.Rel, usage.DirectionWrite, float64(bytes))
		text := fmt.Sprintf("Moved %s to %s", displayPath(src), displayPath(dst))
		return operation.OK(operation.Rendered{Text: text, Data: map[string]any{"src": src.Rel, "dst": dst.Rel, "bytes": bytes}})
	}
}

type globInput struct {
	Pattern    string `json:"pattern" jsonschema:"description=Glob pattern to match.,required"`
	Path       string `json:"path,omitempty" jsonschema:"description=Directory to search from. Defaults to workspace root."`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"description=Maximum number of results."`
}

func (p Plugin) glob(ws system.Workspace) operation.Handler {
	return func(ctx operation.Context, input operation.Value) operation.Result {
		var req globInput
		if err := decode(input, &req); err != nil || strings.TrimSpace(req.Pattern) == "" {
			return operation.Failed("invalid_glob_input", "pattern is required", nil)
		}
		base := req.Path
		if base == "" {
			base = "."
		}
		limit := req.MaxResults
		if limit <= 0 || limit > 5000 {
			limit = 1000
		}
		paths, truncated, err := ws.Glob(ctx, req.Pattern, system.GlobOptions{Base: base, MaxResults: limit})
		if err != nil {
			return operation.Failed("glob_failed", err.Error(), nil)
		}
		matches := make([]string, 0, len(paths))
		for _, matched := range paths {
			matches = append(matches, matched.Rel)
		}
		recordUsage(ctx, GlobOp, base, usage.DirectionRead, float64(len(matches)))
		text := fmt.Sprintf("Matches: %d\n%s", len(matches), strings.Join(matches, "\n"))
		return operation.OK(operation.Rendered{Text: strings.TrimSpace(text), Data: map[string]any{"matches": matches, "truncated": truncated}})
	}
}

// defaultGrepContextLines is the number of surrounding lines returned when the
// caller does not explicitly supply context_lines. A non-zero default makes
// grep results immediately readable without requiring a follow-up file_read.
const defaultGrepContextLines = 3

type grepInput struct {
	Pattern      string   `json:"pattern" jsonschema:"description=Regular expression to search for.,required"`
	Paths        []string `json:"paths,omitempty" jsonschema:"description=Files or directories to search. Defaults to workspace root."`
	ShowContent  bool     `json:"show_content,omitempty" jsonschema:"description=Include matching line content."`
	ContextLines *int     `json:"context_lines,omitempty" jsonschema:"description=Context lines around matches. Defaults to 3."`
	MaxMatches   int      `json:"max_matches,omitempty" jsonschema:"description=Maximum matches to return."`
}

func (p Plugin) grep(ws system.Workspace) operation.Handler {
	return func(ctx operation.Context, input operation.Value) operation.Result {
		var req grepInput
		if err := decode(input, &req); err != nil || strings.TrimSpace(req.Pattern) == "" {
			return operation.Failed("invalid_grep_input", "pattern is required", nil)
		}
		re, err := regexp.Compile(req.Pattern)
		if err != nil {
			return operation.Failed("invalid_grep_pattern", err.Error(), nil)
		}
		ctxLines := defaultGrepContextLines
		if req.ContextLines != nil {
			ctxLines = *req.ContextLines
			if ctxLines < 0 {
				ctxLines = 0
			}
		}
		limit := req.MaxMatches
		if limit <= 0 || limit > 5000 {
			limit = 1000
		}
		roots := req.Paths
		if len(roots) == 0 {
			roots = []string{"."}
		}
		type match struct {
			Path string `json:"path"`
			Line int    `json:"line"`
			Text string `json:"text,omitempty"`
		}
		var matches []match
		for _, raw := range roots {
			resolved, err := ws.ResolveExisting(ctx, raw)
			if err != nil {
				return operation.Failed("grep_failed", err.Error(), nil)
			}
			walk := []system.ResolvedPath{resolved}
			info, _, err := ws.Stat(ctx, raw)
			if err != nil {
				return operation.Failed("grep_failed", err.Error(), nil)
			}
			if info.IsDir() {
				walk = nil
				entries, _, _, err := ws.Walk(ctx, raw, system.WalkOptions{Depth: 50, MaxEntries: limit, FilesOnly: true})
				if err != nil {
					return operation.Failed("grep_failed", err.Error(), nil)
				}
				for _, entry := range entries {
					walk = append(walk, entry.Path)
				}
			}
			for _, file := range walk {
				if len(matches) >= limit {
					break
				}
				data, _, _, err := ws.ReadFile(ctx, file.Rel, maxReadBytes)
				if err != nil || looksBinary(data) {
					continue
				}
				fileLines := strings.Split(string(data), "\n")
				for i, line := range fileLines {
					if re.MatchString(line) {
						item := match{Path: file.Rel, Line: i + 1}
						if req.ShowContent || ctxLines > 0 || req.ContextLines != nil {
							item.Text = grepContextBlock(fileLines, i, ctxLines)
						}
						matches = append(matches, item)
						if len(matches) >= limit {
							break
						}
					}
				}
			}
		}
		lines := []string{fmt.Sprintf("Matches: %d", len(matches))}
		for _, item := range matches {
			if item.Text != "" {
				lines = append(lines, fmt.Sprintf("%s:%d: %s", item.Path, item.Line, item.Text))
			} else {
				lines = append(lines, fmt.Sprintf("%s:%d", item.Path, item.Line))
			}
		}
		recordUsage(ctx, GrepOp, "", usage.DirectionRead, float64(len(matches)))
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"matches": matches, "truncated": len(matches) >= limit}})
	}
}

func decode(input any, out any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func entryType(entry fs.DirEntry) string {
	if entry.IsDir() {
		return "d"
	}
	if entry.Type()&fs.ModeSymlink != 0 {
		return "l"
	}
	return "-"
}

func displayPath(resolved system.ResolvedPath) string {
	if resolved.Rel == "" {
		return "."
	}
	return resolved.Rel
}

func renderFile(resolved system.ResolvedPath, content string, start, end int, lineNumbers, truncated bool) string {
	lines := strings.Split(content, "\n")
	if start <= 0 {
		start = 1
	}
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if start > end {
		start = end
	}
	var out []string
	header := fmt.Sprintf("[file: %s, lines: %d-%d", displayPath(resolved), start, end)
	if truncated {
		header += ", truncated"
	}
	header += "]"
	out = append(out, header)
	for i := start; i <= end && i <= len(lines); i++ {
		if lineNumbers {
			out = append(out, fmt.Sprintf("%6d  %s", i, lines[i-1]))
		} else {
			out = append(out, lines[i-1])
		}
	}
	return strings.Join(out, "\n")
}

func renderFileRange(resolved system.ResolvedPath, content string, firstLine int, lineNumbers, truncated bool) string {
	if firstLine <= 0 {
		firstLine = 1
	}
	lines := []string{}
	if content != "" {
		lines = strings.Split(content, "\n")
	}
	endLine := firstLine
	if len(lines) > 0 {
		endLine = firstLine + len(lines) - 1
	}
	header := fmt.Sprintf("[file: %s, lines: %d-%d", displayPath(resolved), firstLine, endLine)
	if truncated {
		header += ", truncated"
	}
	header += "]"
	out := []string{header}
	for i, line := range lines {
		lineNo := firstLine + i
		if lineNumbers {
			out = append(out, fmt.Sprintf("%6d  %s", lineNo, line))
		} else {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// renderFilePattern searches content for lines matching re and returns a
// rendered block containing each match region with ctxLines of surrounding
// context. Adjacent or overlapping regions are merged into a single block.
func renderFilePattern(resolved system.ResolvedPath, content string, re *regexp.Regexp, ctxLines int, lineNumbers bool) string {
	lines := strings.Split(content, "\n")
	n := len(lines)

	// Collect 0-based matched line indices.
	var matched []int
	for i, l := range lines {
		if re.MatchString(l) {
			matched = append(matched, i)
		}
	}
	if len(matched) == 0 {
		return fmt.Sprintf("[file: %s, pattern: %s, matches: 0]", displayPath(resolved), re.String())
	}

	// Merge match indices into [start, end] regions (0-based, inclusive).
	type region struct{ start, end int }
	var regions []region
	cur := region{
		start: max(0, matched[0]-ctxLines),
		end:   min(n-1, matched[0]+ctxLines),
	}
	for _, idx := range matched[1:] {
		lo := max(0, idx-ctxLines)
		hi := min(n-1, idx+ctxLines)
		if lo <= cur.end+1 {
			// Adjacent or overlapping — extend current region.
			if hi > cur.end {
				cur.end = hi
			}
		} else {
			regions = append(regions, cur)
			cur = region{lo, hi}
		}
	}
	regions = append(regions, cur)

	var out []string
	out = append(out, fmt.Sprintf("[file: %s, pattern: %s, matches: %d]", displayPath(resolved), re.String(), len(matched)))
	for ri, r := range regions {
		if ri > 0 {
			out = append(out, "---")
		}
		for i := r.start; i <= r.end; i++ {
			lineNo := i + 1
			if lineNumbers {
				out = append(out, fmt.Sprintf("%6d  %s", lineNo, lines[i]))
			} else {
				out = append(out, fmt.Sprintf("%d: %s", lineNo, lines[i]))
			}
		}
	}
	return strings.Join(out, "\n")
}

// grepContextBlock returns the matched line plus ctxLines of surrounding lines
// joined into a single string for embedding in a grep match Text field.
func grepContextBlock(lines []string, matchIdx, ctxLines int) string {
	lo := max(0, matchIdx-ctxLines)
	hi := min(len(lines)-1, matchIdx+ctxLines)
	var sb strings.Builder
	for i := lo; i <= hi; i++ {
		if i > lo {
			sb.WriteByte('\n')
		}
		sb.WriteString(lines[i])
	}
	return sb.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type fileLineIndex struct {
	starts []int
	ends   []int
}

func buildLineIndex(content string) fileLineIndex {
	if content == "" {
		return fileLineIndex{}
	}
	starts := []int{0}
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' && i+1 < len(content) {
			starts = append(starts, i+1)
		}
	}
	ends := make([]int, len(starts))
	for i := range starts {
		if i+1 < len(starts) {
			ends[i] = starts[i+1]
		} else {
			ends[i] = len(content)
		}
	}
	return fileLineIndex{starts: starts, ends: ends}
}

func (idx fileLineIndex) lineStart(line int) (int, error) {
	if line < 1 || line > len(idx.starts) {
		return 0, fmt.Errorf("line %d is outside original file line range 1-%d", line, len(idx.starts))
	}
	return idx.starts[line-1], nil
}

func (idx fileLineIndex) lineEnd(line int) (int, error) {
	if line < 1 || line > len(idx.ends) {
		return 0, fmt.Errorf("line %d is outside original file line range 1-%d", line, len(idx.ends))
	}
	return idx.ends[line-1], nil
}

func resolveEditOperation(index int, original string, lineIndex fileLineIndex, raw json.RawMessage) (editFragment, error) {
	var head editOpHead
	if err := json.Unmarshal(raw, &head); err != nil {
		return editFragment{}, fmt.Errorf("operation %d must be an object: %w", index, err)
	}
	op := strings.TrimSpace(head.Op)
	if op == "" {
		return editFragment{}, fmt.Errorf("operation %d op is required", index)
	}
	switch op {
	case "patch":
		var req editPatchOp
		if err := json.Unmarshal(raw, &req); err != nil {
			return editFragment{}, fmt.Errorf("operation %d patch: %w", index, err)
		}
		if req.Old == nil {
			return editFragment{}, fmt.Errorf("operation %d patch old text is required", index)
		}
		if req.New == nil {
			return editFragment{}, fmt.Errorf("operation %d patch new text is required", index)
		}
		if *req.Old == "" {
			return editFragment{}, fmt.Errorf("operation %d patch old text is empty", index)
		}
		start := strings.Index(original, *req.Old)
		if start < 0 {
			return editFragment{Index: index, Op: op, Start: -1, End: -1, Applied: false, Reason: "old text not found"}, fmt.Errorf("operation %d patch old text was not found", index)
		}
		return editFragment{Index: index, Op: op, Start: start, End: start + len(*req.Old), New: *req.New, Line: findLine(original, *req.Old), OldText: *req.Old, SortOrder: index}, nil
	case "insert_after":
		var req editLineInsertAfterOp
		if err := json.Unmarshal(raw, &req); err != nil {
			return editFragment{}, fmt.Errorf("operation %d %s: %w", index, op, err)
		}
		if req.Content == nil {
			return editFragment{}, fmt.Errorf("operation %d %s content is required", index, op)
		}
		pos, err := lineIndex.lineEnd(req.Line)
		if err != nil {
			return editFragment{}, fmt.Errorf("operation %d %s: %w", index, op, err)
		}
		return editFragment{Index: index, Op: op, Start: pos, End: pos, New: *req.Content, Line: req.Line, SortOrder: index}, nil
	case "insert_before":
		var req editLineInsertBeforeOp
		if err := json.Unmarshal(raw, &req); err != nil {
			return editFragment{}, fmt.Errorf("operation %d %s: %w", index, op, err)
		}
		if req.Content == nil {
			return editFragment{}, fmt.Errorf("operation %d %s content is required", index, op)
		}
		pos, err := lineIndex.lineStart(req.Line)
		if err != nil {
			return editFragment{}, fmt.Errorf("operation %d %s: %w", index, op, err)
		}
		return editFragment{Index: index, Op: op, Start: pos, End: pos, New: *req.Content, Line: req.Line, SortOrder: index}, nil
	case "replace_range":
		var req editRangeReplaceOp
		if err := json.Unmarshal(raw, &req); err != nil {
			return editFragment{}, fmt.Errorf("operation %d %s: %w", index, op, err)
		}
		if req.Content == nil {
			return editFragment{}, fmt.Errorf("operation %d %s content is required", index, op)
		}
		if req.StartLine <= 0 || req.EndLine <= 0 || req.StartLine > req.EndLine {
			return editFragment{}, fmt.Errorf("operation %d %s requires start_line <= end_line and both >= 1", index, op)
		}
		start, err := lineIndex.lineStart(req.StartLine)
		if err != nil {
			return editFragment{}, fmt.Errorf("operation %d %s: %w", index, op, err)
		}
		end, err := lineIndex.lineEnd(req.EndLine)
		if err != nil {
			return editFragment{}, fmt.Errorf("operation %d %s: %w", index, op, err)
		}
		return editFragment{Index: index, Op: op, Start: start, End: end, New: *req.Content, Line: req.StartLine, EndLine: req.EndLine, OldText: original[start:end], SortOrder: index}, nil
	case "delete_range":
		var req editRangeDeleteOp
		if err := json.Unmarshal(raw, &req); err != nil {
			return editFragment{}, fmt.Errorf("operation %d %s: %w", index, op, err)
		}
		if req.StartLine <= 0 || req.EndLine <= 0 || req.StartLine > req.EndLine {
			return editFragment{}, fmt.Errorf("operation %d %s requires start_line <= end_line and both >= 1", index, op)
		}
		start, err := lineIndex.lineStart(req.StartLine)
		if err != nil {
			return editFragment{}, fmt.Errorf("operation %d %s: %w", index, op, err)
		}
		end, err := lineIndex.lineEnd(req.EndLine)
		if err != nil {
			return editFragment{}, fmt.Errorf("operation %d %s: %w", index, op, err)
		}
		return editFragment{Index: index, Op: op, Start: start, End: end, New: "", Line: req.StartLine, EndLine: req.EndLine, OldText: original[start:end], SortOrder: index}, nil
	case "append":
		var req editAppendOp
		if err := json.Unmarshal(raw, &req); err != nil {
			return editFragment{}, fmt.Errorf("operation %d %s: %w", index, op, err)
		}
		if req.Content == nil {
			return editFragment{}, fmt.Errorf("operation %d %s content is required", index, op)
		}
		pos := len(original)
		return editFragment{Index: index, Op: op, Start: pos, End: pos, New: *req.Content, SortOrder: index}, nil
	case "prepend":
		var req editPrependOp
		if err := json.Unmarshal(raw, &req); err != nil {
			return editFragment{}, fmt.Errorf("operation %d %s: %w", index, op, err)
		}
		if req.Content == nil {
			return editFragment{}, fmt.Errorf("operation %d %s content is required", index, op)
		}
		return editFragment{Index: index, Op: op, Start: 0, End: 0, New: *req.Content, SortOrder: index}, nil
	default:
		return editFragment{}, fmt.Errorf("operation %d has unknown op %q", index, op)
	}
}

func validateEditFragments(fragments []editFragment) error {
	for i := 0; i < len(fragments); i++ {
		for j := i + 1; j < len(fragments); j++ {
			if editFragmentsConflict(fragments[i], fragments[j]) {
				return fmt.Errorf("operation %d (%s) overlaps operation %d (%s)", fragments[i].Index, fragments[i].Op, fragments[j].Index, fragments[j].Op)
			}
		}
	}
	return nil
}

func editFragmentsConflict(a, b editFragment) bool {
	aInsert := a.Start == a.End
	bInsert := b.Start == b.End
	if aInsert && bInsert {
		return false
	}
	if !aInsert && !bInsert {
		return a.Start < b.End && b.Start < a.End
	}
	if aInsert {
		return b.Start < a.Start && a.Start < b.End
	}
	return a.Start < b.Start && b.Start < a.End
}

func mergeEditFragments(original string, fragments []editFragment) string {
	ordered := append([]editFragment(nil), fragments...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Start != ordered[j].Start {
			return ordered[i].Start < ordered[j].Start
		}
		iInsert := ordered[i].Start == ordered[i].End
		jInsert := ordered[j].Start == ordered[j].End
		if iInsert != jInsert {
			return iInsert
		}
		return ordered[i].SortOrder < ordered[j].SortOrder
	})
	var out strings.Builder
	cursor := 0
	for _, fragment := range ordered {
		if fragment.Start > cursor {
			out.WriteString(original[cursor:fragment.Start])
			cursor = fragment.Start
		}
		out.WriteString(fragment.New)
		if fragment.End > cursor {
			cursor = fragment.End
		}
	}
	out.WriteString(original[cursor:])
	return out.String()
}

// diffOp kinds.
const (
	diffEqual = iota
	diffDelete
	diffInsert
)

type diffLineOp struct {
	kind    int
	text    string
	oldLine int
	newLine int
}

// diffLines computes a line-level edit script from a to b using LCS.
func diffLines(a, b []string) []diffLineOp {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] > dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var ops []diffLineOp
	i, j := 0, 0
	for i < n || j < m {
		if i < n && j < m && a[i] == b[j] {
			ops = append(ops, diffLineOp{diffEqual, a[i], i + 1, j + 1})
			i++
			j++
		} else if j < m && (i >= n || dp[i][j+1] >= dp[i+1][j]) {
			ops = append(ops, diffLineOp{diffInsert, b[j], 0, j + 1})
			j++
		} else {
			ops = append(ops, diffLineOp{diffDelete, a[i], i + 1, 0})
			i++
		}
	}
	return ops
}

// splitDiffLines splits text into lines, stripping the trailing empty element
// left by a terminal newline.
func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// unifiedDiff produces a compact unified diff between before and after with
// 3 lines of context around each changed region.
func unifiedDiff(path, before, after string) string {
	if before == after {
		return ""
	}
	const ctxLines = 3

	ops := diffLines(splitDiffLines(before), splitDiffLines(after))
	total := len(ops)

	// Mark which op indices belong to a hunk (within ctxLines of a change).
	inHunk := make([]bool, total)
	for i, op := range ops {
		if op.kind == diffEqual {
			continue
		}
		lo, hi := i-ctxLines, i+ctxLines
		if lo < 0 {
			lo = 0
		}
		if hi >= total {
			hi = total - 1
		}
		for k := lo; k <= hi; k++ {
			inHunk[k] = true
		}
	}

	// Group consecutive marked ops into hunks.
	type hunkOps []diffLineOp
	var hunks []hunkOps
	for i := 0; i < total; {
		if !inHunk[i] {
			i++
			continue
		}
		start := i
		for i < total && inHunk[i] {
			i++
		}
		hunks = append(hunks, ops[start:i])
	}
	if len(hunks) == 0 {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n", path)
	fmt.Fprintf(&sb, "+++ %s\n", path)
	for _, h := range hunks {
		oldStart, newStart, oldCount, newCount := 0, 0, 0, 0
		for _, op := range h {
			switch op.kind {
			case diffEqual:
				if oldStart == 0 {
					oldStart = op.oldLine
				}
				if newStart == 0 {
					newStart = op.newLine
				}
				oldCount++
				newCount++
			case diffDelete:
				if oldStart == 0 {
					oldStart = op.oldLine
				}
				oldCount++
			case diffInsert:
				if newStart == 0 {
					newStart = op.newLine
				}
				newCount++
			}
		}
		if oldStart == 0 {
			oldStart = 1
		}
		if newStart == 0 {
			newStart = 1
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
		for _, op := range h {
			switch op.kind {
			case diffEqual:
				fmt.Fprintf(&sb, " %s\n", op.text)
			case diffDelete:
				fmt.Fprintf(&sb, "-%s\n", op.text)
			case diffInsert:
				fmt.Fprintf(&sb, "+%s\n", op.text)
			}
		}
	}
	return sb.String()
}

// findLine returns the 1-based line number at which old first appears in text.
// Returns -1 if not found.
func findLine(text, old string) int {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.Contains(line, old) {
			return i + 1
		}
	}
	// old may span multiple lines; find the first line of the match.
	idx := strings.Index(text, old)
	if idx < 0 {
		return -1
	}
	return strings.Count(text[:idx], "\n") + 1
}

func looksBinary(data []byte) bool {
	sample := data
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	for _, b := range sample {
		if b == 0 {
			return true
		}
	}
	return false
}

func recordUsage(ctx operation.Context, source, path string, direction usage.Direction, quantity float64) {
	if quantity < 0 {
		quantity = 0
	}
	ctx.Events().Emit(usage.Recorded{
		Source: source,
		Subject: usage.Subject{
			Kind: usage.SubjectFile,
			Name: path,
		},
		Measurements: []usage.Measurement{{
			Metric:    usage.MetricFileBytes,
			Quantity:  quantity,
			Unit:      usage.UnitByte,
			Direction: direction,
		}},
	})
}
