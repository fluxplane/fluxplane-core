//go:build !linux

package human

import (
	"context"
	"fmt"
	"runtime"
)

// SpeakPiperBackground is currently available only on Linux builds.
func SpeakPiperBackground(context.Context, string) error {
	return fmt.Errorf("piper text-to-speech is unavailable on %s", runtime.GOOS)
}
