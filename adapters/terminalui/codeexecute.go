package terminalui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/plugins/codeplugin"
)

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

func codeExecuteResultData(result operation.Result) (codeplugin.ExecuteResult, bool) {
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		return codeplugin.ExecuteResult{}, false
	}
	var exec codeplugin.ExecuteResult
	payload, err := json.Marshal(rendered.Data)
	if err != nil {
		return codeplugin.ExecuteResult{}, false
	}
	if err := json.Unmarshal(payload, &exec); err != nil {
		return codeplugin.ExecuteResult{}, false
	}
	if exec.Preset == "" && exec.Image == "" && exec.Stdout == "" && exec.Stderr == "" && exec.ExitCode == 0 && exec.DurationMS == 0 {
		return codeplugin.ExecuteResult{}, false
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
