package system

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/amitybell/piper"
	voice "github.com/amitybell/piper-voice-jenny"
)

var (
	piperOnce sync.Once
	piperInst *piper.TTS
	piperErr  error
)

// SpeakPiperBackground synthesizes text with the embedded Piper engine and
// queues playback through a local audio player. It returns after synthesis and
// player launch, not after audio playback completes.
func SpeakPiperBackground(ctx context.Context, text string) error {
	tts, err := initPiper()
	if err != nil {
		return fmt.Errorf("piper init: %w", err)
	}
	wav, err := tts.Synthesize(text)
	if err != nil {
		return fmt.Errorf("piper synthesize: %w", err)
	}
	tmp, err := os.CreateTemp("", "fluxplane-piper-*.wav")
	if err != nil {
		return fmt.Errorf("piper: create temp file: %w", err)
	}
	if _, err := tmp.Write(wav); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("piper: write wav: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("piper: close wav: %w", err)
	}
	if err := playWAVBackground(ctx, tmp.Name()); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

func initPiper() (*piper.TTS, error) {
	piperOnce.Do(func() {
		piperInst, piperErr = piper.NewEmbedded("", voice.Asset)
	})
	return piperInst, piperErr
}

func playWAVBackground(ctx context.Context, path string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for _, args := range [][]string{
		{"aplay", "-q", path},
		{"paplay", path},
	} {
		if _, err := exec.LookPath(args[0]); err != nil {
			continue
		}
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		configureCommandProcess(cmd)
		if err := cmd.Start(); err != nil {
			continue
		}
		done := make(chan struct{})
		go func() {
			defer os.Remove(path)
			defer close(done)
			_ = cmd.Wait()
		}()
		go func() {
			select {
			case <-ctx.Done():
				terminateCommandProcess(cmd)
			case <-done:
			}
		}()
		return nil
	}
	return fmt.Errorf("no audio player available (need aplay or paplay)")
}
