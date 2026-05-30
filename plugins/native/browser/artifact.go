package browser

import (
	"context"
	"fmt"
	"path"

	browserapi "github.com/fluxplane/fluxplane-browser"
	fpsystem "github.com/fluxplane/fluxplane-system"
)

type fileSystemArtifactWriter struct {
	fileSystem  fpsystem.FileSystem
	artifactDir string
}

func (w fileSystemArtifactWriter) WriteArtifact(ctx context.Context, req browserapi.ArtifactWriteRequest) (browserapi.Artifact, error) {
	if w.fileSystem == nil {
		return browserapi.Artifact{}, fmt.Errorf("browser artifact filesystem is nil")
	}
	dir := w.artifactDir
	if dir == "" {
		dir = path.Join(".agents", "artifacts", "browser")
	}
	name, err := w.fileSystem.WriteTempFile(ctx, path.Join(dir, req.SessionID), req.Prefix+"-*"+req.Ext, req.Data, fpsystem.WriteTempFileOptions{Perm: 0o644})
	if err != nil {
		return browserapi.Artifact{}, err
	}
	description := req.Description
	if description == "" {
		description = req.Prefix
	}
	return browserapi.Artifact{SessionID: req.SessionID, Path: name, MediaType: req.MediaType, Bytes: len(req.Data), Description: description}, nil
}
