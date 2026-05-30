package launch

import (
	"context"
	"fmt"
	"path"
	"time"

	browser "github.com/fluxplane/fluxplane-browser"
	"github.com/fluxplane/fluxplane-core/runtime/system"
)

type browserArtifactWriter struct {
	workspace   system.Workspace
	artifactDir string
}

func (w browserArtifactWriter) WriteArtifact(ctx context.Context, req browser.ArtifactWriteRequest) (browser.Artifact, error) {
	if w.workspace == nil {
		return browser.Artifact{}, fmt.Errorf("browser artifact workspace is nil")
	}
	dir := w.artifactDir
	if dir == "" {
		dir = path.Join(".agents", "artifacts", "browser")
	}
	name := path.Join(dir, req.SessionID, fmt.Sprintf("%s-%d%s", req.Prefix, time.Now().UnixNano(), req.Ext))
	resolved, err := w.workspace.WriteFile(ctx, name, req.Data, 0o644, true)
	if err != nil {
		return browser.Artifact{}, err
	}
	description := req.Description
	if description == "" {
		description = req.Prefix
	}
	return browser.Artifact{SessionID: req.SessionID, Path: resolved.Rel, MediaType: req.MediaType, Bytes: len(req.Data), Description: description}, nil
}
