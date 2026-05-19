package dockerplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimeenvironment "github.com/fluxplane/agentruntime/runtime/environment"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	Name               = "docker"
	ObserverName       = "docker.status"
	SignalDeriverName  = "docker.signals"
	ObservationStatus  = "docker.status"
	SignalConfigured   = "integration.configured"
	SignalAvailable    = "integration.available"
	defaultStatusScope = "local"
)

// Plugin contributes Docker environment observation and signals.
type Plugin struct {
	system system.System
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.ObserverContributor = Plugin{}
var _ pluginhost.SignalDeriverContributor = Plugin{}

// New returns a Docker integration plugin.
func New(sys system.System) Plugin { return Plugin{system: sys} }

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Docker environment observation."}
}

// Contributions returns Docker observation resources.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		Observers:      []coreenvironment.ObserverSpec{observerSpec()},
		SignalDerivers: []coreenvironment.SignalDeriverSpec{signalDeriverSpec()},
	}, nil
}

// EnvironmentObservers returns executable Docker observers.
func (p Plugin) EnvironmentObservers(context.Context, pluginhost.Context) ([]runtimeenvironment.Observer, error) {
	return []runtimeenvironment.Observer{dockerObserver{system: p.system}}, nil
}

// SignalDerivers returns executable Docker signal derivation.
func (Plugin) SignalDerivers(context.Context, pluginhost.Context) ([]runtimeenvironment.SignalDeriver, error) {
	return []runtimeenvironment.SignalDeriver{dockerSignalDeriver{}}, nil
}

// Status records non-secret Docker availability facts.
type Status struct {
	BinaryAvailable  bool   `json:"binary_available"`
	DaemonAvailable  bool   `json:"daemon_available"`
	ClientVersion    string `json:"client_version,omitempty"`
	ServerVersion    string `json:"server_version,omitempty"`
	Diagnostic       string `json:"diagnostic,omitempty"`
	DaemonDiagnostic string `json:"daemon_diagnostic,omitempty"`
}

type dockerObserver struct {
	system system.System
}

func (dockerObserver) Spec() coreenvironment.ObserverSpec {
	return observerSpec()
}

func (o dockerObserver) Observe(ctx context.Context, _ runtimeenvironment.ObservationRequest) ([]coreenvironment.Observation, error) {
	status := observeDockerStatus(ctx, o.system)
	return []coreenvironment.Observation{{
		ID:          "integration:docker:local",
		Environment: coreenvironment.Ref{Name: coreenvironment.Name(Name)},
		Kind:        ObservationStatus,
		Scope:       defaultStatusScope,
		Content:     status,
		At:          time.Now().UTC(),
	}}, nil
}

func observerSpec() coreenvironment.ObserverSpec {
	return coreenvironment.ObserverSpec{
		Name:        ObserverName,
		Description: "Reports non-secret Docker CLI and daemon availability.",
		Environment: coreenvironment.Ref{
			Name: coreenvironment.Name(Name),
		},
		Phase:           coreenvironment.PhaseTurn,
		ObservableKinds: []string{ObservationStatus},
		Dynamic:         true,
		Annotations:     map[string]string{"plugin": Name},
	}
}

type dockerSignalDeriver struct{}

func (dockerSignalDeriver) Spec() coreenvironment.SignalDeriverSpec {
	return signalDeriverSpec()
}

func (dockerSignalDeriver) Derive(_ context.Context, req runtimeenvironment.SignalDeriveRequest) ([]coreenvironment.Signal, error) {
	var out []coreenvironment.Signal
	for _, observation := range req.Observations {
		if observation.Kind != ObservationStatus {
			continue
		}
		status, ok := dockerStatusFromObservation(observation.Content)
		if !ok {
			continue
		}
		metadata := dockerSignalMetadata(status)
		if status.BinaryAvailable {
			out = append(out, coreenvironment.Signal{
				Kind:           SignalConfigured,
				Target:         Name,
				Scope:          observation.Scope,
				Environment:    observation.Environment,
				Confidence:     1,
				ObservationIDs: observationIDs(observation.ID),
				Metadata:       metadata,
			})
		}
		if status.DaemonAvailable {
			out = append(out, coreenvironment.Signal{
				Kind:           SignalAvailable,
				Target:         Name,
				Scope:          observation.Scope,
				Environment:    observation.Environment,
				Confidence:     1,
				ObservationIDs: observationIDs(observation.ID),
				Metadata:       metadata,
			})
		}
	}
	return out, nil
}

func signalDeriverSpec() coreenvironment.SignalDeriverSpec {
	return coreenvironment.SignalDeriverSpec{
		Name:             SignalDeriverName,
		Description:      "Derives Docker integration configured/available signals from Docker status observations.",
		ObservationKinds: []string{ObservationStatus},
		Signals: []coreenvironment.SignalTemplate{
			{Kind: SignalConfigured, Target: Name},
			{Kind: SignalAvailable, Target: Name},
		},
	}
}

func observeDockerStatus(ctx context.Context, sys system.System) Status {
	status := Status{}
	if sys == nil || sys.Process() == nil {
		status.Diagnostic = "process manager is unavailable"
		return status
	}
	process := sys.Process()
	versionRun, err := process.Run(ctx, system.ProcessRequest{
		Command:   "docker",
		Args:      []string{"--version"},
		Timeout:   2 * time.Second,
		MaxStdout: 4096,
		MaxStderr: 4096,
	})
	if err != nil || versionRun.ExitCode != 0 {
		status.Diagnostic = boundedDiagnostic(err, versionRun)
		return status
	}
	status.BinaryAvailable = true
	status.ClientVersion = parseDockerVersionText(versionRun.Stdout)
	daemonRun, daemonErr := process.Run(ctx, system.ProcessRequest{
		Command:   "docker",
		Args:      []string{"version", "--format", "{{json .}}"},
		Timeout:   3 * time.Second,
		MaxStdout: 8192,
		MaxStderr: 8192,
	})
	if daemonErr != nil || daemonRun.ExitCode != 0 {
		status.DaemonDiagnostic = boundedDiagnostic(daemonErr, daemonRun)
		return status
	}
	status.DaemonAvailable = true
	applyDockerVersionJSON(&status, daemonRun.Stdout)
	return status
}

func dockerStatusFromObservation(content any) (Status, bool) {
	switch typed := content.(type) {
	case Status:
		return typed, true
	case *Status:
		if typed == nil {
			return Status{}, false
		}
		return *typed, true
	default:
		return Status{}, false
	}
}

func dockerSignalMetadata(status Status) map[string]string {
	metadata := map[string]string{}
	if strings.TrimSpace(status.ClientVersion) != "" {
		metadata["client_version"] = strings.TrimSpace(status.ClientVersion)
	}
	if strings.TrimSpace(status.ServerVersion) != "" {
		metadata["server_version"] = strings.TrimSpace(status.ServerVersion)
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func parseDockerVersionText(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	const prefix = "Docker version "
	if strings.HasPrefix(out, prefix) {
		out = strings.TrimPrefix(out, prefix)
	}
	if idx := strings.Index(out, ","); idx >= 0 {
		out = out[:idx]
	}
	return strings.TrimSpace(out)
}

func applyDockerVersionJSON(status *Status, raw string) {
	if status == nil {
		return
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &data); err != nil {
		return
	}
	if version := nestedString(data, "Client", "Version"); version != "" {
		status.ClientVersion = version
	}
	if version := nestedString(data, "Server", "Version"); version != "" {
		status.ServerVersion = version
	}
}

func nestedString(data map[string]any, keys ...string) string {
	var current any = data
	for _, key := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = m[key]
	}
	value, _ := current.(string)
	return strings.TrimSpace(value)
}

func boundedDiagnostic(err error, result system.ProcessResult) string {
	var parts []string
	if err != nil {
		parts = append(parts, err.Error())
	}
	if out := strings.TrimSpace(result.Stderr); out != "" {
		parts = append(parts, out)
	}
	if out := strings.TrimSpace(result.Stdout); out != "" {
		parts = append(parts, out)
	}
	if len(parts) == 0 && result.ExitCode != 0 {
		parts = append(parts, fmt.Sprintf("docker exited with code %d", result.ExitCode))
	}
	diagnostic := strings.Join(parts, "\n")
	if len(diagnostic) > 512 {
		diagnostic = diagnostic[:512]
	}
	return diagnostic
}

func observationIDs(id string) []string {
	if id == "" {
		return nil
	}
	return []string{id}
}
