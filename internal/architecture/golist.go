package architecture

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

func LoadGoList(ctx context.Context, dir string) ([]ListedPackage, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-json", "./...")
	cmd.Dir = dir
	raw, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("go list failed: %w\n%s", err, string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("go list failed: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	var packages []ListedPackage
	for decoder.More() {
		var pkg ListedPackage
		if err := decoder.Decode(&pkg); err != nil {
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		packages = append(packages, pkg)
	}
	return packages, nil
}
