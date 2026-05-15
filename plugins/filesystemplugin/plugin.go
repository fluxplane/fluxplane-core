package filesystemplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/operation"
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
	FilePatchOp   = "file_patch"
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
		operationruntime.NewTypedResult[dirCreateInput, operation.Rendered](specByName(DirCreateOp), func(ctx operation.Context, req dirCreateInput) operation.Result { return p.dirCreate(ws)(ctx, req) }),
		operationruntime.NewTypedResult[dirListInput, operation.Rendered](specByName(DirListOp), func(ctx operation.Context, req dirListInput) operation.Result { return p.dirList(ws)(ctx, req) }),
		operationruntime.NewTypedResult[dirTreeInput, operation.Rendered](specByName(DirTreeOp), func(ctx operation.Context, req dirTreeInput) operation.Result { return p.dirTree(ws)(ctx, req) }),
		operationruntime.NewTypedResult[fileReadInput, operation.Rendered](specByName(FileReadOp), func(ctx operation.Context, req fileReadInput) operation.Result { return p.fileRead(ws)(ctx, req) }),
		operationruntime.NewTypedResult[fileCreateInput, operation.Rendered](specByName(FileCreateOp), func(ctx operation.Context, req fileCreateInput) operation.Result { return p.fileCreate(ws)(ctx, req) }),
		operationruntime.NewTypedResult[filePatchInput, operation.Rendered](specByName(FilePatchOp), func(ctx operation.Context, req filePatchInput) operation.Result { return p.filePatch(ws)(ctx, req) }),
		operationruntime.NewTypedResult[pathInput, operation.Rendered](specByName(FileDeleteOp), func(ctx operation.Context, req pathInput) operation.Result { return p.fileDelete(ws)(ctx, req) }),
		operationruntime.NewTypedResult[pathInput, operation.Rendered](specByName(FileStatOp), func(ctx operation.Context, req pathInput) operation.Result { return p.fileStat(ws)(ctx, req) }),
		operationruntime.NewTypedResult[copyMoveInput, operation.Rendered](specByName(FileCopyOp), func(ctx operation.Context, req copyMoveInput) operation.Result { return p.fileCopy(ws)(ctx, req) }),
		operationruntime.NewTypedResult[copyMoveInput, operation.Rendered](specByName(FileMoveOp), func(ctx operation.Context, req copyMoveInput) operation.Result { return p.fileMove(ws)(ctx, req) }),
		operationruntime.NewTypedResult[globInput, operation.Rendered](specByName(GlobOp), func(ctx operation.Context, req globInput) operation.Result { return p.glob(ws)(ctx, req) }),
		operationruntime.NewTypedResult[grepInput, operation.Rendered](specByName(GrepOp), func(ctx operation.Context, req grepInput) operation.Result { return p.grep(ws)(ctx, req) }),
	}, nil
}

func specs() []operation.Spec {
	return []operation.Spec{
		spec[dirCreateInput, operation.Rendered](DirCreateOp, "Create a workspace directory, including parents.", operation.EffectFilesystem, operation.EffectCreate, operation.EffectWriteExternal),
		spec[dirListInput, operation.Rendered](DirListOp, "List a workspace directory.", operation.EffectFilesystem, operation.EffectReadExternal),
		spec[dirTreeInput, operation.Rendered](DirTreeOp, "Render a bounded workspace directory tree.", operation.EffectFilesystem, operation.EffectReadExternal),
		spec[fileReadInput, operation.Rendered](FileReadOp, "Read a bounded workspace file with optional line ranges.", operation.EffectFilesystem, operation.EffectReadExternal),
		spec[fileCreateInput, operation.Rendered](FileCreateOp, "Create or overwrite a workspace file, creating parent directories.", operation.EffectFilesystem, operation.EffectCreate, operation.EffectWriteExternal),
		spec[filePatchInput, operation.Rendered](FilePatchOp, "Patch a workspace file with exact text replacements, optionally dry-run.", operation.EffectFilesystem, operation.EffectUpdate, operation.EffectWriteExternal),
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

type filePatchInput struct {
	Path    string      `json:"path" jsonschema:"description=File path to patch. Globs are not expanded here.,required"`
	Old     string      `json:"old,omitempty" jsonschema:"description=Single old text to replace."`
	New     string      `json:"new,omitempty" jsonschema:"description=Single replacement text."`
	Patches []textPatch `json:"patches,omitempty" jsonschema:"description=Exact text replacements to apply in order."`
	DryRun  bool        `json:"dry_run,omitempty" jsonschema:"description=Return diff without writing changes."`
}

type textPatch struct {
	Old string `json:"old" jsonschema:"description=Exact old text.,required"`
	New string `json:"new" jsonschema:"description=Replacement text.,required"`
}

// patchStatus reports the outcome of one patch application.
type patchStatus struct {
	Matched bool   `json:"matched"`
	Line    int    `json:"line"`   // 1-based line where old text was found; -1 when not found
	Reason  string `json:"reason"` // empty on success
}

func (p Plugin) filePatch(ws system.Workspace) operation.Handler {
	return func(ctx operation.Context, input operation.Value) operation.Result {
		var req filePatchInput
		if err := decode(input, &req); err != nil || strings.TrimSpace(req.Path) == "" {
			return operation.Failed("invalid_file_patch_input", "path is required", nil)
		}
		if len(req.Patches) == 0 && (req.Old != "" || req.New != "") {
			req.Patches = []textPatch{{Old: req.Old, New: req.New}}
		}
		if len(req.Patches) == 0 {
			return operation.Failed("invalid_file_patch_input", "at least one patch is required", nil)
		}
		fileData, truncated, resolved, err := ws.ReadFile(ctx, req.Path, maxWriteBytes)
		if err != nil {
			return operation.Failed("file_patch_failed", err.Error(), nil)
		}
		if truncated {
			return operation.Failed("file_patch_too_large", "file exceeds patch read limit", map[string]any{"path": resolved.Rel, "max_bytes": maxWriteBytes})
		}
		before := string(fileData)
		after := before
		statuses := make([]patchStatus, len(req.Patches))
		for i, patch := range req.Patches {
			if patch.Old == "" {
				return operation.Failed("invalid_file_patch_input", fmt.Sprintf("patch %d old text is empty", i), nil)
			}
			if !strings.Contains(after, patch.Old) {
				statuses[i] = patchStatus{Matched: false, Line: -1, Reason: "old text not found"}
				return operation.Failed("file_patch_no_match",
					fmt.Sprintf("patch %d old text was not found", i),
					map[string]any{"path": resolved.Rel, "patches": statuses[:i+1]})
			}
			statuses[i] = patchStatus{Matched: true, Line: findLine(after, patch.Old), Reason: ""}
			after = strings.Replace(after, patch.Old, patch.New, 1)
		}
		diff := unifiedDiff(resolved.Rel, before, after)
		if !req.DryRun {
			if _, err := ws.WriteFile(ctx, req.Path, []byte(after), 0644, true); err != nil {
				return operation.Failed("file_patch_failed", err.Error(), nil)
			}
			recordUsage(ctx, FilePatchOp, resolved.Rel, usage.DirectionWrite, float64(len(after)))
		}
		action := "Would patch"
		if !req.DryRun {
			action = "Patched"
		}
		text := fmt.Sprintf("%s %s\n\n%s", action, displayPath(resolved), diff)
		modelText := fmt.Sprintf("%s %s: %d replacement(s) applied. Diff omitted from model transcript; use git_diff or file_read if exact content is needed.", action, displayPath(resolved), len(req.Patches))
		return operation.OK(operation.Rendered{Text: text, Model: modelText, Data: map[string]any{"path": resolved.Rel, "dry_run": req.DryRun, "patches": statuses, "diff": diff}})
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
			resolved, err := ws.ResolveExisting(raw)
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
