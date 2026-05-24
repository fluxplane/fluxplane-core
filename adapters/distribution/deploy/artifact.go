package deploy

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

const artifactIndexVersion = 1

// ArtifactIndex records build outputs that deploy targets can consume without
// rerendering app resources.
type ArtifactIndex struct {
	Version  int             `json:"version"`
	AppDir   string          `json:"app_dir"`
	Profile  string          `json:"profile,omitempty"`
	Profiles []string        `json:"profiles,omitempty"`
	Targets  []BuildArtifact `json:"targets"`
}

// BuildArtifact describes one generated build artifact.
type BuildArtifact struct {
	Target  string   `json:"target"`
	Kind    string   `json:"kind"`
	Path    string   `json:"path,omitempty"`
	Paths   []string `json:"paths,omitempty"`
	Image   string   `json:"image,omitempty"`
	Command []string `json:"command,omitempty"`
}

func artifactIndexPath(appRoot string) string {
	return filepath.Join(appDeployDir(appRoot), "fluxplane-build.json")
}

func writeArtifactIndex(appRoot string, index ArtifactIndex, dryRun bool, out io.Writer) error {
	sort.Slice(index.Targets, func(i, j int) bool {
		return index.Targets[i].Target < index.Targets[j].Target
	})
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("distribution build: marshal artifact index: %w", err)
	}
	filename := artifactIndexPath(appRoot)
	if dryRun {
		_, _ = fmt.Fprintf(out, "write=%s\n", filename)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filename, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return ensureGitignoreEntry(appRoot, ".deploy/")
}

func readArtifactIndex(appRoot string) (ArtifactIndex, error) {
	data, err := os.ReadFile(artifactIndexPath(appRoot))
	if err != nil {
		return ArtifactIndex{}, err
	}
	var index ArtifactIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return ArtifactIndex{}, fmt.Errorf("distribution deploy: decode artifact index: %w", err)
	}
	if index.Version != artifactIndexVersion {
		return ArtifactIndex{}, fmt.Errorf("distribution deploy: unsupported artifact index version %d", index.Version)
	}
	return index, nil
}

func artifactByTarget(index ArtifactIndex, name string) (BuildArtifact, bool) {
	for _, artifact := range index.Targets {
		if artifact.Target == name {
			return artifact, true
		}
	}
	return BuildArtifact{}, false
}
