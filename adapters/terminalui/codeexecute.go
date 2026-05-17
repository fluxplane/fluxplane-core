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
	status := fmt.Sprintf("%s %s code executed successfully", emoji, label)
	if exec.TimedOut {
		status = fmt.Sprintf("❌ %s %s code timed out", emoji, label)
	} else if result.Status != "" && result.Status != operation.StatusOK || result.Error != nil || exec.ExitCode != 0 {
		status = fmt.Sprintf("❌ %s %s code failed", emoji, label)
	}

	lines := []string{"  " + status}
	if strings.TrimSpace(exec.Preset) != "" {
		lines = append(lines, "     preset: "+exec.Preset)
	}
	if strings.TrimSpace(exec.Image) != "" {
		lines = append(lines, "     image: "+exec.Image)
	}
	duration := time.Duration(exec.DurationMS) * time.Millisecond
	if duration <= 0 {
		duration = fallbackDuration
	}
	if duration > 0 {
		lines = append(lines, "     duration: "+formatCodeExecuteDuration(duration))
	}
	lines = append(lines, fmt.Sprintf("     exit: %d", exec.ExitCode))
	if exec.TimedOut && exec.TimeoutMS > 0 {
		lines = append(lines, "     timeout: "+formatCodeExecuteDuration(time.Duration(exec.TimeoutMS)*time.Millisecond))
	}
	if exec.StdoutTruncated {
		lines = append(lines, "     stdout: truncated")
	}
	if exec.StderrTruncated {
		lines = append(lines, "     stderr: truncated")
	}

	stdout := strings.TrimRight(exec.Stdout, "\n")
	stderr := strings.TrimRight(exec.Stderr, "\n")
	if stdout == "" && stderr == "" {
		lines = append(lines, "", "     (no stdout or stderr)")
	} else {
		if stdout != "" {
			lines = append(lines, "", "     stdout", "     ────────────────────────────────────────")
			lines = appendIndentedBlock(lines, stdout, "     ")
		}
		if stderr != "" {
			lines = append(lines, "", "     stderr", "     ────────────────────────────────────────")
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
		return "🐍", "Python"
	case "go":
		return "🐹", "Go"
	case "node":
		return "🟩", "Node.js"
	default:
		return "📦", "Code"
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
