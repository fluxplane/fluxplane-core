package agentsdk

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"unicode"

	"github.com/fluxplane/agentruntime/adapters/appconfig"
	"github.com/spf13/cobra"
)

//go:embed templates/minimal-app.yaml
var minimalManifestTemplate string

type initOptions struct {
	force bool
}

func newInitCommand() *cobra.Command {
	var opts initOptions
	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Create a minimal local app manifest",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) > 0 {
				path = args[0]
			}
			manifestPath, err := Init(path, InitOptions{Force: opts.force})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "created %s\n", manifestPath)
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.force, "force", false, "overwrite an existing agentsdk app manifest")
	return cmd
}

type InitOptions struct {
	Force bool
}

func Init(path string, opts InitOptions) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	dir, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("init: create %s: %w", dir, err)
	}
	existing, err := existingManifest(dir)
	if err != nil {
		return "", err
	}
	manifestPath := filepath.Join(dir, "agentsdk.app.yaml")
	if existing != "" && !opts.Force {
		return "", fmt.Errorf("init: manifest already exists at %s (use --force to overwrite)", existing)
	}
	if existing != "" && filepath.Clean(existing) != manifestPath {
		return "", fmt.Errorf("init: manifest already exists at %s; remove it before writing %s", existing, manifestPath)
	}
	data, err := renderMinimalManifest(filepath.Base(dir))
	if err != nil {
		return "", err
	}
	if _, err := appconfig.DecodeFile(manifestPath, data); err != nil {
		return "", fmt.Errorf("init: generated manifest is invalid: %w", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		return "", fmt.Errorf("init: write %s: %w", manifestPath, err)
	}
	return manifestPath, nil
}

func existingManifest(dir string) (string, error) {
	for _, name := range appconfig.DefaultManifestNames {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("init: inspect %s: %w", path, err)
		}
	}
	return "", nil
}

func renderMinimalManifest(name string) ([]byte, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "local"
	}
	data := struct {
		Name   string
		Socket string
	}{
		Name:   name,
		Socket: "agentsdk-" + slug(name) + ".sock",
	}
	tpl, err := template.New("minimal-app.yaml").Funcs(template.FuncMap{
		"quote": strconv.Quote,
	}).Parse(minimalManifestTemplate)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := tpl.Execute(&out, data); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func slug(value string) string {
	var out strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(value) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			out.WriteRune(unicode.ToLower(r))
			lastDash = false
		case r == '-' || r == '_':
			out.WriteRune(r)
			lastDash = false
		default:
			if out.Len() > 0 && !lastDash {
				out.WriteByte('-')
				lastDash = true
			}
		}
	}
	result := strings.Trim(out.String(), "-_")
	if result == "" {
		return "local"
	}
	return result
}
