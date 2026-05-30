package docker

import (
	"context"
	"encoding/json"
	"fmt"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"strings"
	"time"

	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
)

const (
	Name                 = "docker"
	ObserverName         = "docker.status"
	AssertionDeriverName = "docker.assertions"
	ObservationStatus    = "docker.status"
	AssertionConfigured  = "integration.configured"
	AssertionAvailable   = "integration.available"
	defaultStatusScope   = "local"
)

// Plugin contributes Docker environment observation and assertions.
type Plugin struct {
	process fpsystem.ProcessManager
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.ObserverContributor = Plugin{}
var _ pluginhost.AssertionDeriverContributor = Plugin{}

// New returns a Docker integration plugin.
func New(sys fpsystem.System) Plugin { return Plugin{process: processFromSystem(sys)} }

// NewWithProcess returns a Docker integration plugin using an explicit process boundary.
func NewWithProcess(process fpsystem.ProcessManager) Plugin { return Plugin{process: process} }

func processFromSystem(sys fpsystem.System) fpsystem.ProcessManager {
	if sys == nil {
		return nil
	}
	return sys.Process()
}

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Docker environment observation."}
}

// Contributions returns Docker observation resources.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		Observers:         []coreevidence.ObserverSpec{observerSpec()},
		AssertionDerivers: []coreevidence.AssertionDeriverSpec{assertionDeriverSpec()},
	}, nil
}

// EnvironmentObservers returns executable Docker observers.
func (p Plugin) EnvironmentObservers(context.Context, pluginhost.Context) ([]runtimeevidence.Observer, error) {
	return []runtimeevidence.Observer{dockerObserver(p)}, nil
}

// AssertionDerivers returns executable Docker assertion derivation.
func (Plugin) AssertionDerivers(context.Context, pluginhost.Context) ([]runtimeevidence.AssertionDeriver, error) {
	return []runtimeevidence.AssertionDeriver{dockerAssertionDeriver{}}, nil
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
	process fpsystem.ProcessManager
}

func (dockerObserver) Spec() coreevidence.ObserverSpec {
	return observerSpec()
}

func (o dockerObserver) Observe(ctx context.Context, _ runtimeevidence.ObservationRequest) ([]coreevidence.Observation, error) {
	status := observeDockerStatus(ctx, o.process)
	return []coreevidence.Observation{{
		ID:          "integration:docker:local",
		Environment: coreevidence.Ref{Name: coreevidence.Name(Name)},
		Kind:        ObservationStatus,
		Scope:       defaultStatusScope,
		Content:     status,
		At:          time.Now().UTC(),
	}}, nil
}

func observerSpec() coreevidence.ObserverSpec {
	return coreevidence.ObserverSpec{
		Name:        ObserverName,
		Description: "Reports non-secret Docker CLI and daemon availability.",
		Environment: coreevidence.Ref{
			Name: coreevidence.Name(Name),
		},
		Phase:           coreevidence.PhaseTurn,
		ObservableKinds: []string{ObservationStatus},
		Dynamic:         true,
		Annotations:     map[string]string{"plugin": Name},
	}
}

type dockerAssertionDeriver struct{}

func (dockerAssertionDeriver) Spec() coreevidence.AssertionDeriverSpec {
	return assertionDeriverSpec()
}

func (dockerAssertionDeriver) Derive(_ context.Context, req runtimeevidence.AssertionDeriveRequest) ([]coreevidence.Assertion, error) {
	var out []coreevidence.Assertion
	for _, observation := range req.Observations {
		if observation.Kind != ObservationStatus {
			continue
		}
		status, ok := dockerStatusFromObservation(observation.Content)
		if !ok {
			continue
		}
		metadata := dockerAssertionMetadata(status)
		if status.BinaryAvailable {
			out = append(out, coreevidence.Assertion{
				Kind:           AssertionConfigured,
				Target:         Name,
				Subject:        coreevidence.Subject{Kind: coreevidence.SubjectIntegration, Name: Name},
				Scope:          observation.Scope,
				Environment:    observation.Environment,
				Confidence:     1,
				ObservationIDs: observationIDs(observation.ID),
				Metadata:       metadata,
			})
		}
		if status.DaemonAvailable {
			out = append(out, coreevidence.Assertion{
				Kind:           AssertionAvailable,
				Target:         Name,
				Subject:        coreevidence.Subject{Kind: coreevidence.SubjectIntegration, Name: Name},
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

func assertionDeriverSpec() coreevidence.AssertionDeriverSpec {
	return coreevidence.AssertionDeriverSpec{
		Name:             AssertionDeriverName,
		Description:      "Derives Docker integration configured/available assertions from Docker status observations.",
		ObservationKinds: []string{ObservationStatus},
		Assertions: []coreevidence.AssertionTemplate{
			{Kind: AssertionConfigured, Target: Name, Subject: coreevidence.Subject{Kind: coreevidence.SubjectIntegration, Name: Name}},
			{Kind: AssertionAvailable, Target: Name, Subject: coreevidence.Subject{Kind: coreevidence.SubjectIntegration, Name: Name}},
		},
	}
}

func observeDockerStatus(ctx context.Context, process fpsystem.ProcessManager) Status {
	status := Status{}
	if process == nil {
		status.Diagnostic = "process manager is unavailable"
		return status
	}
	versionRun, err := process.Run(ctx, fpsystem.ProcessRequest{
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
	daemonRun, daemonErr := process.Run(ctx, fpsystem.ProcessRequest{
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

func dockerAssertionMetadata(status Status) map[string]string {
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
	out = strings.TrimPrefix(out, prefix)
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

func boundedDiagnostic(err error, result fpsystem.ProcessResult) string {
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
