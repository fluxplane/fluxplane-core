package terminal

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fluxplane/fluxplane-operation"
)

type codeExecuteResult struct {
	Preset          string   `json:"preset"`
	Image           string   `json:"image"`
	Files           []string `json:"files,omitempty"`
	Command         []string `json:"command,omitempty"`
	Stdout          string   `json:"stdout,omitempty"`
	Stderr          string   `json:"stderr,omitempty"`
	ExitCode        int      `json:"exit_code"`
	TimedOut        bool     `json:"timed_out,omitempty"`
	DurationMS      int64    `json:"duration_ms"`
	TimeoutMS       int64    `json:"timeout_ms,omitempty"`
	StdoutTruncated bool     `json:"stdout_truncated,omitempty"`
	StderrTruncated bool     `json:"stderr_truncated,omitempty"`
}

func renderCodeExecuteResult(result operation.Result, fallbackDuration time.Duration) (string, bool) {
	exec, ok := codeExecuteResultData(result)
	if !ok {
		return "", false
	}
	emoji, label := codeExecutePresetDisplay(exec.Preset)
	duration := time.Duration(exec.DurationMS) * time.Millisecond
	if duration <= 0 {
		duration = fallbackDuration
	}

	header := []string{"  🧪 code_execute", emoji + " " + label}
	if strings.TrimSpace(exec.Image) != "" {
		header = append(header, "📦 "+exec.Image)
	}
	if duration > 0 {
		header = append(header, "⏱️ "+formatCodeExecuteDuration(duration))
	}
	if exec.TimedOut {
		if exec.TimeoutMS > 0 {
			header = append(header, "⏳ timeout "+formatCodeExecuteDuration(time.Duration(exec.TimeoutMS)*time.Millisecond))
		} else {
			header = append(header, "⏳ timeout")
		}
	}
	if exec.TimedOut || result.Status != "" && result.Status != operation.StatusOK || result.Error != nil || exec.ExitCode != 0 {
		header = append(header, fmt.Sprintf("❌ exit %d", exec.ExitCode))
	}

	lines := []string{strings.Join(header, "  ")}
	stdout := strings.TrimRight(exec.Stdout, "\n")
	stderr := strings.TrimRight(exec.Stderr, "\n")
	if stdout == "" && stderr == "" {
		lines = append(lines, "     ∅ no output")
	} else {
		if stdout != "" {
			label := "📤 stdout"
			if exec.StdoutTruncated {
				label += " (truncated)"
			}
			lines = append(lines, "", "     "+label)
			lines = appendIndentedBlock(lines, stdout, "     ")
		}
		if stderr != "" {
			label := "⚠️ stderr"
			if exec.StderrTruncated {
				label += " (truncated)"
			}
			lines = append(lines, "", "     "+label)
			lines = appendIndentedBlock(lines, stderr, "     ")
		}
	}
	return strings.Join(lines, "\n") + "\n", true
}

func codeExecuteResultData(result operation.Result) (codeExecuteResult, bool) {
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		return codeExecuteResult{}, false
	}
	var exec codeExecuteResult
	payload, err := json.Marshal(rendered.Data)
	if err != nil {
		return codeExecuteResult{}, false
	}
	if err := json.Unmarshal(payload, &exec); err != nil {
		return codeExecuteResult{}, false
	}
	if exec.Preset == "" && exec.Image == "" && exec.Stdout == "" && exec.Stderr == "" && exec.ExitCode == 0 && exec.DurationMS == 0 {
		return codeExecuteResult{}, false
	}
	return exec, true
}

func codeExecutePresetDisplay(preset string) (string, string) {
	switch preset {
	case "python":
		return "🐍", "python"
	case "go":
		return "🐹", "go"
	case "node":
		return "🟩", "node"
	default:
		if strings.TrimSpace(preset) != "" {
			return "📦", preset
		}
		return "📦", "code"
	}
}

func formatCodeExecuteDuration(duration time.Duration) string {
	if duration < time.Second {
		return duration.Round(time.Millisecond).String()
	}
	return fmt.Sprintf("%.1fs", duration.Seconds())
}

func appendIndentedBlock(lines []string, text, indent string) []string {
	for _, line := range strings.Split(text, "\n") {
		lines = append(lines, indent+line)
	}
	return lines
}
