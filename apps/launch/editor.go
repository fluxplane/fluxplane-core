package launch

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// EditorRunner opens path in an editor.
type EditorRunner func(ctx context.Context, path string, in io.Reader, out, errOut io.Writer) error

// OpenEditor opens path using VISUAL, EDITOR, or vi.
func OpenEditor(ctx context.Context, path string, in io.Reader, out, errOut io.Writer) error {
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	if len(parts) == 0 {
		return fmt.Errorf("editor: empty editor command")
	}
	args := append(append([]string(nil), parts[1:]...), path)
	cmd := exec.CommandContext(ctx, parts[0], args...)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor %q: %w", parts[0], err)
	}
	return nil
}
