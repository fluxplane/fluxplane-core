package gitlab

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/operation"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

type SnippetActionInput struct {
	Op          string                `json:"op" jsonschema:"description=Snippet action.,enum=create,enum=delete,required"`
	SnippetID   int64                 `json:"snippet_id,omitempty" jsonschema:"description=Snippet id for delete."`
	Title       string                `json:"title,omitempty" jsonschema:"description=Snippet title for create."`
	Description string                `json:"description,omitempty" jsonschema:"description=Snippet description for create."`
	Visibility  string                `json:"visibility,omitempty" jsonschema:"description=Snippet visibility.,enum=private,enum=internal,enum=public"`
	Files       []SnippetFileArgument `json:"files,omitempty" jsonschema:"description=Snippet files for create."`
}

type SnippetFileArgument struct {
	FilePath string `json:"file_path" jsonschema:"description=Snippet file path.,required"`
	Content  string `json:"content" jsonschema:"description=Snippet file content.,required"`
}

type SnippetActionResult struct {
	Op         string `json:"op"`
	SnippetID  int64  `json:"snippet_id,omitempty"`
	Title      string `json:"title,omitempty"`
	Visibility string `json:"visibility,omitempty"`
	WebURL     string `json:"web_url,omitempty"`
	Message    string `json:"message,omitempty"`
}

func (p Plugin) snippetOperationSpec() operation.Spec {
	return gitlabWriteSpecWithEffects(
		p.operationName(snippetOp),
		"Create or delete personal GitLab snippets.",
		operation.RiskCritical,
		operation.EffectSet{
			operation.EffectNetwork,
			operation.EffectWriteExternal,
			operation.EffectCreate,
			operation.EffectDelete,
			operation.EffectDestructive,
		},
		operationruntime.TypeOf[SnippetActionInput](p.operationName(snippetOp)+"_input"),
		operationruntime.TypeOf[SnippetActionResult](p.operationName(snippetOp)+"_output"),
	)
}

func (p Plugin) snippetOperation() operation.Operation {
	return operationruntime.NewTypedResult[SnippetActionInput, SnippetActionResult](
		p.snippetOperationSpec(),
		p.runSnippetAction,
		operationruntime.WithAccess(p.snippetAccess),
	)
}

func (p Plugin) runSnippetAction(ctx operation.Context, req SnippetActionInput) operation.Result {
	req.Op = strings.ToLower(strings.TrimSpace(req.Op))
	client, err := p.client(ctx)
	if err != nil {
		return operation.Failed(p.operationName(snippetOp)+"_failed", err.Error(), nil)
	}
	result, err := executeSnippetAction(ctx, client, req)
	if err != nil {
		return operation.Failed(p.operationName(snippetOp)+"_failed", err.Error(), nil)
	}
	return operation.OK(result)
}

func executeSnippetAction(ctx operation.Context, client gitlabClient, req SnippetActionInput) (SnippetActionResult, error) {
	switch req.Op {
	case "create":
		if strings.TrimSpace(req.Title) == "" {
			return SnippetActionResult{}, fmt.Errorf("title is required for create")
		}
		files, err := createSnippetFiles(req.Files)
		if err != nil {
			return SnippetActionResult{}, err
		}
		opts := &gitlab.CreateSnippetOptions{
			Title: gitlab.Ptr(strings.TrimSpace(req.Title)),
			Files: &files,
		}
		if description := strings.TrimSpace(req.Description); description != "" {
			opts.Description = gitlab.Ptr(description)
		}
		visibility, err := snippetVisibility(req.Visibility)
		if err != nil {
			return SnippetActionResult{}, err
		}
		opts.Visibility = &visibility
		snippet, err := client.CreateSnippet(ctx, opts)
		if err != nil {
			return SnippetActionResult{}, err
		}
		return snippetActionResult(req.Op, snippet, "snippet created"), nil
	case "delete":
		if req.SnippetID == 0 {
			return SnippetActionResult{}, fmt.Errorf("snippet_id is required for delete")
		}
		if err := client.DeleteSnippet(ctx, req.SnippetID); err != nil {
			return SnippetActionResult{}, err
		}
		return SnippetActionResult{Op: req.Op, SnippetID: req.SnippetID, Message: "snippet deleted"}, nil
	default:
		return SnippetActionResult{}, fmt.Errorf("unsupported op %q", req.Op)
	}
}

func createSnippetFiles(values []SnippetFileArgument) ([]*gitlab.CreateSnippetFileOptions, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("files are required for create")
	}
	out := make([]*gitlab.CreateSnippetFileOptions, 0, len(values))
	for _, value := range values {
		path := strings.TrimSpace(value.FilePath)
		if path == "" {
			return nil, fmt.Errorf("files file_path is required")
		}
		out = append(out, &gitlab.CreateSnippetFileOptions{
			FilePath: gitlab.Ptr(path),
			Content:  gitlab.Ptr(value.Content),
		})
	}
	return out, nil
}

func snippetVisibility(value string) (gitlab.VisibilityValue, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "private"
	}
	switch value {
	case "private", "internal", "public":
		return gitlab.VisibilityValue(value), nil
	default:
		return "", fmt.Errorf("invalid visibility %q", value)
	}
}

func snippetActionResult(op string, snippet *gitlab.Snippet, message string) SnippetActionResult {
	out := SnippetActionResult{Op: op, Message: message}
	if snippet == nil {
		return out
	}
	out.SnippetID = snippet.ID
	if out.SnippetID == 0 && strings.TrimSpace(snippet.FileName) != "" {
		if id, err := strconv.ParseInt(snippet.FileName, 10, 64); err == nil {
			out.SnippetID = id
		}
	}
	out.Title = snippet.Title
	out.Visibility = snippet.Visibility
	out.WebURL = snippet.WebURL
	return out
}

func (p Plugin) snippetAccess(operation.Context, SnippetActionInput) ([]operationruntime.AccessDescriptor, error) {
	return p.gitlabNetworkWriteAccess(nil, nil)
}
