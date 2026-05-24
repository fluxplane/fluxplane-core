package deploy

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestGenerateKubernetesManifestsReferencesExternalEnvSecret(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
runtime:
  workspace:
    env_files:
      - .env
      - .env.local
distribution:
  build:
    assets: [fluxplane.yaml]
    docker:
      image: sample
      tags: [latest]
---
kind: agent
name: assistant
`)
	writeTestFile(t, app, ".env", "OPENROUTER_API_KEY=secret-one\nSHARED=first\n")
	writeTestFile(t, app, ".env.local", "SHARED=last\n")

	result, err := GenerateKubernetesManifests(context.Background(), KubernetesManifestOptions{
		AppDir: app,
		Image:  "sample:test",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("GenerateKubernetesManifests: %v", err)
	}
	for _, want := range []string{
		"  name: sample-env",
		"        envFrom:",
		"            name: sample-env",
		"        image: sample:test",
		"        - --effort",
		"        - medium",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("kubernetes manifest missing %q:\n%s", want, result.Content)
		}
	}
	for _, leaked := range []string{"kind: Secret", "secret-one", "SHARED: last"} {
		if strings.Contains(result.Content, leaked) {
			t.Fatalf("kubernetes manifest leaked %q:\n%s", leaked, result.Content)
		}
	}
}

func TestGenerateKubernetesManifestsHonorsEnvSecretNameOverride(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
runtime:
  workspace:
    env_files:
      - .env
---
kind: agent
name: assistant
`)
	result, err := GenerateKubernetesManifests(context.Background(), KubernetesManifestOptions{
		AppDir:        app,
		Image:         "sample:test",
		EnvSecretName: "platform-managed-secret",
		DryRun:        true,
	})
	if err != nil {
		t.Fatalf("GenerateKubernetesManifests: %v", err)
	}
	if result.SecretName != "platform-managed-secret" || !strings.Contains(result.Content, "name: platform-managed-secret") {
		t.Fatalf("secret name/content = %q\n%s", result.SecretName, result.Content)
	}
}

func TestGenerateKubernetesManifestsReferencesRuntimeSecret(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: support-bot
runtime:
  data:
    store:
      kind: mysql
      dsn_env: FLUXPLANE_DATASTORE_MYSQL_DSN
  events:
    store:
      kind: nats
      dsn_env: FLUXPLANE_EVENTSTORE_NATS_DSN
distribution:
  build:
    assets: [fluxplane.yaml]
    docker:
      image: support-bot
      tags: [local]
---
kind: agent
name: assistant
`)
	result, err := GenerateKubernetesManifests(context.Background(), KubernetesManifestOptions{
		AppDir: app,
		Image:  "support-bot:test",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("GenerateKubernetesManifests: %v", err)
	}
	for _, want := range []string{
		"        envFrom:",
		"            name: fluxplane-stack",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("kubernetes manifest missing %q:\n%s", want, result.Content)
		}
	}
	for _, leaked := range []string{"kind: StatefulSet", "name: mysql", "name: nats", "fluxplane:fluxplane", "nats://nats:4222"} {
		if strings.Contains(result.Content, leaked) {
			t.Fatalf("kubernetes manifest leaked runtime backend %q:\n%s", leaked, result.Content)
		}
	}
}

func TestGenerateKubernetesManifestsIncludesNodeSelector(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  build:
    assets: [fluxplane.yaml]
    docker:
      image: sample
      tags: [latest]
---
kind: agent
name: assistant
`)
	result, err := GenerateKubernetesManifests(context.Background(), KubernetesManifestOptions{
		AppDir:        app,
		Image:         "sample:test",
		NodeSelectors: []string{"group=workers", "kubernetes.io/arch=amd64"},
		DryRun:        true,
	})
	if err != nil {
		t.Fatalf("GenerateKubernetesManifests: %v", err)
	}
	for _, want := range []string{
		"      nodeSelector:",
		"        group: workers",
		"        kubernetes.io/arch: amd64",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("kubernetes manifest missing %q:\n%s", want, result.Content)
		}
	}
}

func TestGenerateKubernetesManifestsUsesImagePullPolicy(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  build:
    assets: [fluxplane.yaml]
    docker:
      image: sample
      tags: [latest]
---
kind: agent
name: assistant
`)
	result, err := GenerateKubernetesManifests(context.Background(), KubernetesManifestOptions{
		AppDir:          app,
		Image:           "sample:test",
		ImagePullPolicy: "Always",
		DryRun:          true,
	})
	if err != nil {
		t.Fatalf("GenerateKubernetesManifests: %v", err)
	}
	if !strings.Contains(result.Content, "        imagePullPolicy: Always") {
		t.Fatalf("kubernetes manifest missing imagePullPolicy Always:\n%s", result.Content)
	}
}

func TestGenerateKubernetesManifestsIncludesKubernetesPluginRBAC(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
plugins:
  kubernetes:
    namespaces:
      - latest
      - monitoring
distribution:
  build:
    assets: [fluxplane.yaml]
    docker:
      image: sample
      tags: [latest]
---
kind: agent
name: assistant
`)
	result, err := GenerateKubernetesManifests(context.Background(), KubernetesManifestOptions{
		AppDir:    app,
		Image:     "sample:test",
		Namespace: "ai-bots",
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("GenerateKubernetesManifests: %v", err)
	}
	for _, want := range []string{
		"kind: ServiceAccount",
		"  name: sample",
		"  namespace: ai-bots",
		"kind: Role",
		"  namespace: latest",
		"  namespace: monitoring",
		"  resources:",
		"  - pods",
		"  - services",
		"  - deployments",
		"kind: RoleBinding",
		"  name: sample",
		"  namespace: ai-bots",
		"      serviceAccountName: sample",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("kubernetes manifest missing %q:\n%s", want, result.Content)
		}
	}
}

func TestGenerateKubernetesManifestsRejectsNamedRootEnvFiles(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
runtime:
  workspace:
    roots:
      - name: tmp
        path: /tmp/fluxplane-test
        env_files: [.env.tmp]
distribution:
  build:
    assets: [fluxplane.yaml]
    docker: {}
---
kind: agent
name: assistant
`)
	_, err := GenerateKubernetesManifests(context.Background(), KubernetesManifestOptions{
		AppDir: app,
		DryRun: true,
	})
	if err == nil || !strings.Contains(err.Error(), `does not support env_files on workspace root "tmp"`) {
		t.Fatalf("GenerateKubernetesManifests error = %v, want named root env_files error", err)
	}
}

func TestKubernetesPureObjectHelpers(t *testing.T) {
	content := `
apiVersion: v1
kind: Namespace
metadata:
  name: ai-bots
---
not: valid enough to map
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
  namespace: ai-bots
`
	objects := decodeKubernetesObjects(content)
	if len(objects) != 3 {
		t.Fatalf("objects len = %d, want 3", len(objects))
	}
	resource, namespaced, err := kubernetesObjectResource(objects[0])
	if err != nil || resource.Resource != "namespaces" || namespaced {
		t.Fatalf("namespace resource=%#v namespaced=%v err=%v", resource, namespaced, err)
	}
	resource, namespaced, err = kubernetesObjectResource(objects[2])
	if err != nil || resource.Group != "apps" || resource.Resource != "deployments" || !namespaced {
		t.Fatalf("deployment resource=%#v namespaced=%v err=%v", resource, namespaced, err)
	}
	_, _, err = kubernetesObjectResource(&unstructured.Unstructured{Object: map[string]any{"apiVersion": "batch/v1", "kind": "Job"}})
	if err == nil || !strings.Contains(err.Error(), "unsupported kubernetes object") {
		t.Fatalf("unsupported resource error = %v, want unsupported object", err)
	}

	for _, object := range []*unstructured.Unstructured{
		{Object: map[string]any{"apiVersion": "v1", "kind": "Secret"}},
		{Object: map[string]any{"apiVersion": "v1", "kind": "Service"}},
		{Object: map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaim"}},
		{Object: map[string]any{"apiVersion": "v1", "kind": "ServiceAccount"}},
		{Object: map[string]any{"apiVersion": "apps/v1", "kind": "StatefulSet"}},
		{Object: map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "Role"}},
		{Object: map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "RoleBinding"}},
		{Object: map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole"}},
		{Object: map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRoleBinding"}},
	} {
		if resource, _, err := kubernetesObjectResource(object); err != nil || resource.Resource == "" {
			t.Fatalf("kubernetesObjectResource(%s/%s) = %#v, %v", object.GetAPIVersion(), object.GetKind(), resource, err)
		}
	}
}

func TestKubernetesIdentityAndConfigHelpers(t *testing.T) {
	if !boolFromConfig(" YES ") || !boolFromConfig(true) || boolFromConfig("no") {
		t.Fatal("boolFromConfig returned unexpected decisions")
	}
	namespaces := namespacesFromConfig([]any{"prod, staging", []string{"dev", "prod"}})
	if got := strings.Join(normalizeKubernetesNamespaces(namespaces), ","); got != "dev,prod,staging" {
		t.Fatalf("namespaces = %q, want sorted unique list", got)
	}
	for _, rendered := range []string{
		kubernetesPVCIdentities("ai-bots", []string{"data-mysql-0"}),
		kubernetesServiceAccountIdentity("ai-bots", "app"),
		strings.Join(kubernetesRBACIdentities("ai-bots", "app", kubernetesRBACSpec{Namespaces: []string{"prod"}}), "\n"),
		strings.Join(kubernetesRBACIdentities("ai-bots", "app", kubernetesRBACSpec{AllNamespaces: true}), "\n"),
		kubernetesRoleIdentity("prod", "reader"),
		kubernetesRoleBindingIdentity("prod", "reader"),
		kubernetesClusterRoleIdentity("reader"),
		kubernetesClusterRoleBindingIdentity("reader"),
	} {
		if !strings.Contains(rendered, "kind:") {
			t.Fatalf("rendered identity missing kind:\n%s", rendered)
		}
	}
}

func TestDeployKubernetesDryRunRedactsEnvSecretValues(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
runtime:
  workspace:
    env_files: [.env]
distribution:
  build:
    assets: [fluxplane.yaml]
    docker:
      image: sample
      tags: [latest]
---
kind: agent
name: assistant
`)
	writeTestFile(t, app, ".env", "OPENROUTER_API_KEY=supersecret\n")
	var out bytes.Buffer
	result, err := DeployKubernetes(context.Background(), KubernetesOptions{
		AppDir:  app,
		TempDir: t.TempDir(),
		Image:   "sample:test",
		DryRun:  true,
		Out:     &out,
	})
	if err != nil {
		t.Fatalf("DeployKubernetes dry-run: %v", err)
	}
	if !result.DryRun || result.SecretName != "sample-env" {
		t.Fatalf("result = %#v", result)
	}
	manifest := filepath.Join(app, ".deploy", "kubernetes.yaml")
	for _, want := range []string{
		"write=" + manifest,
		"secret=sample-env external=true",
		"command=kubectl apply -f <registry-manifest>",
		"command=kubectl rollout status deployment/coder-registry -n sample --timeout=120s",
		"command=kubectl port-forward -n sample --address 127.0.0.1 service/coder-registry 5000:5000",
		"command=docker tag sample:test 127.0.0.1:5000/sample:test",
		"command=docker push 127.0.0.1:5000/sample:test",
		"command=kubectl apply -f " + manifest,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "supersecret") {
		t.Fatalf("dry-run output leaked secret:\n%s", out.String())
	}
}

func TestDeployKubernetesNamespaceRegistryFlow(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  build:
    assets: [fluxplane.yaml]
    docker:
      image: sample
      tags: [latest]
---
kind: agent
name: assistant
`)
	runner := &recordingRunner{}
	forwarder := &recordingPortForwarder{}
	result, err := DeployKubernetes(context.Background(), KubernetesOptions{
		AppDir:        app,
		TempDir:       t.TempDir(),
		Image:         "sample:test",
		RegistryMode:  "namespace",
		Force:         true,
		Runner:        runner,
		PortForwarder: forwarder,
	})
	if err != nil {
		t.Fatalf("DeployKubernetes: %v", err)
	}
	if result.Image != "coder-registry.sample.svc.cluster.local:5000/sample:test" {
		t.Fatalf("image = %q", result.Image)
	}
	if forwarder.namespace != "sample" || !forwarder.closed {
		t.Fatalf("port forwarder namespace=%q closed=%v", forwarder.namespace, forwarder.closed)
	}
	manifest := filepath.Join(app, ".deploy", "kubernetes.yaml")
	joined := strings.Join(runner.calls, "\n")
	for _, want := range []string{
		"docker build -t fluxplane/fluxplane-base:local ",
		"docker build -t sample:test -f " + filepath.Join(app, "Dockerfile") + " " + app,
		"kubectl apply -f ",
		"kubectl rollout status deployment/coder-registry -n sample --timeout=120s",
		"docker tag sample:test 127.0.0.1:5000/sample:test",
		"docker push 127.0.0.1:5000/sample:test",
		"kubectl apply -f " + manifest,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("commands missing %q:\n%s", want, joined)
		}
	}
	if _, err := os.Stat(filepath.Join(app, "kubernetes.yaml")); !os.IsNotExist(err) {
		t.Fatalf("root kubernetes.yaml exists after deploy: %v", err)
	}
	gitignore, err := os.ReadFile(filepath.Join(app, ".gitignore"))
	if err != nil {
		t.Fatalf("ReadFile .gitignore: %v", err)
	}
	if !strings.Contains(string(gitignore), ".deploy/") {
		t.Fatalf(".gitignore = %q, want .deploy/", gitignore)
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("ReadFile manifest: %v", err)
	}
	for _, want := range []string{
		"  name: coder-registry",
		"        image: coder-registry.sample.svc.cluster.local:5000/sample:test",
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("manifest missing %q:\n%s", want, data)
		}
	}
}

func TestDeployKubernetesAutoUsesK3DImportForK3DContext(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  build:
    assets: [fluxplane.yaml]
    docker:
      image: sample
      tags: [latest]
---
kind: agent
name: assistant
`)
	originalDetect := detectKubernetesContext
	detectKubernetesContext = func(context.Context) (string, error) { return "k3d-sample-cluster", nil }
	defer func() { detectKubernetesContext = originalDetect }()

	runner := &recordingRunner{}
	result, err := DeployKubernetes(context.Background(), KubernetesOptions{
		AppDir:  app,
		TempDir: t.TempDir(),
		Image:   "sample:test",
		Force:   true,
		Runner:  runner,
	})
	if err != nil {
		t.Fatalf("DeployKubernetes: %v", err)
	}
	if result.Image != "sample:test" {
		t.Fatalf("image = %q", result.Image)
	}
	manifest := filepath.Join(app, ".deploy", "kubernetes.yaml")
	joined := strings.Join(runner.calls, "\n")
	for _, want := range []string{
		"k3d image import sample:test --cluster sample-cluster",
		"kubectl apply -f " + manifest,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("commands missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "coder-registry") || strings.Contains(joined, "127.0.0.1:5000") {
		t.Fatalf("k3d deploy used namespace registry path:\n%s", joined)
	}
}

func TestDeployKubernetesExternalRegistryDryRunSkipsNamespaceRegistry(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  build:
    assets: [fluxplane.yaml]
    docker:
      image: sample
      tags: [latest]
---
kind: agent
name: assistant
`)
	var out bytes.Buffer
	result, err := DeployKubernetes(context.Background(), KubernetesOptions{
		AppDir:       app,
		TempDir:      t.TempDir(),
		Image:        "sample:test",
		RegistryMode: "external",
		Registry:     "123456789012.dkr.ecr.us-east-1.amazonaws.com/ai-bots",
		DryRun:       true,
		Out:          &out,
	})
	if err != nil {
		t.Fatalf("DeployKubernetes external dry-run: %v", err)
	}
	if result.Image != "123456789012.dkr.ecr.us-east-1.amazonaws.com/ai-bots/sample:test" {
		t.Fatalf("image = %q", result.Image)
	}
	if strings.Contains(out.String(), "coder-registry") || strings.Contains(out.String(), "<registry-manifest>") {
		t.Fatalf("external registry dry-run included namespace registry:\n%s", out.String())
	}
	manifest := filepath.Join(app, ".deploy", "kubernetes.yaml")
	for _, want := range []string{
		"command=docker tag sample:test 123456789012.dkr.ecr.us-east-1.amazonaws.com/ai-bots/sample:test",
		"command=docker push 123456789012.dkr.ecr.us-east-1.amazonaws.com/ai-bots/sample:test",
		"command=kubectl apply -f " + manifest,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out.String())
		}
	}
}

func TestDeployKubernetesExternalRegistryPreservesImageRepositoryPath(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  build:
    assets: [fluxplane.yaml]
    docker:
      image: ai-agents/slack-bot
      tags: [latest]
---
kind: agent
name: assistant
`)
	var out bytes.Buffer
	result, err := DeployKubernetes(context.Background(), KubernetesOptions{
		AppDir:       app,
		TempDir:      t.TempDir(),
		Image:        "ai-agents/slack-bot:latest",
		RegistryMode: "external",
		Registry:     "523757638725.dkr.ecr.eu-central-1.amazonaws.com",
		DryRun:       true,
		Out:          &out,
	})
	if err != nil {
		t.Fatalf("DeployKubernetes external dry-run: %v", err)
	}
	wantImage := "523757638725.dkr.ecr.eu-central-1.amazonaws.com/ai-agents/slack-bot:latest"
	if result.Image != wantImage {
		t.Fatalf("image = %q, want %q", result.Image, wantImage)
	}
	for _, want := range []string{
		"command=docker tag ai-agents/slack-bot:latest " + wantImage,
		"command=docker push " + wantImage,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out.String())
		}
	}
}

func TestUndeployKubernetesDryRunPreservesPVCsAndSkipsEnvFiles(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: support-bot
runtime:
  workspace:
    env_files: [.env]
  data:
    store:
      kind: mysql
  events:
    store:
      kind: nats
distribution:
  build:
    assets: [fluxplane.yaml]
    docker: {}
---
kind: agent
name: assistant
`)
	var calls []string
	var out bytes.Buffer
	runner := CommandRunnerFunc(func(_ context.Context, _ string, name string, args []string, _, _ io.Writer) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	})

	result, err := UndeployKubernetes(context.Background(), KubernetesUndeployOptions{
		AppDir:    app,
		Namespace: "ai-bots",
		DryRun:    true,
		Out:       &out,
		Runner:    runner,
	})
	if err != nil {
		t.Fatalf("UndeployKubernetes dry-run: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("dry-run calls = %#v, want none", calls)
	}
	if result.Namespace != "ai-bots" {
		t.Fatalf("namespace = %q, want ai-bots", result.Namespace)
	}
	if !strings.Contains(out.String(), "command=kubectl delete -f <kubernetes-teardown-manifest> --ignore-not-found") {
		t.Fatalf("dry-run output = %q", out.String())
	}
	if strings.Contains(out.String(), "delete pvc") {
		t.Fatalf("dry-run should preserve PVCs:\n%s", out.String())
	}
}

func TestUndeployKubernetesDryRunDoesNotDeleteSharedRuntimePVCsWithVolumes(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: support-bot
runtime:
  data:
    store:
      kind: mysql
  events:
    store:
      kind: nats
distribution:
  build:
    assets: [fluxplane.yaml]
    docker: {}
---
kind: agent
name: assistant
`)
	var out bytes.Buffer
	result, err := UndeployKubernetes(context.Background(), KubernetesUndeployOptions{
		AppDir:    app,
		Namespace: "ai-bots",
		DryRun:    true,
		Volumes:   true,
		Out:       &out,
	})
	if err != nil {
		t.Fatalf("UndeployKubernetes dry-run: %v", err)
	}
	if strings.Contains(out.String(), "delete pvc") || strings.Contains(out.String(), "data-mysql-0") || strings.Contains(out.String(), "data-nats-0") {
		t.Fatalf("dry-run should not delete shared runtime PVCs:\n%s", out.String())
	}
	if strings.Contains(out.String(), "coder-registry") {
		t.Fatalf("dry-run should not delete shared namespace registry:\n%s", out.String())
	}
	if !result.Volumes || len(result.Commands) != 1 {
		t.Fatalf("result = %#v, want only app teardown command", result)
	}
}

func TestUndeployKubernetesRunsDeleteWithGeneratedManifest(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: support-bot
runtime:
  workspace:
    env_files: [.env]
  data:
    store:
      kind: mysql
distribution:
  build:
    assets: [fluxplane.yaml]
    docker: {}
---
kind: agent
name: assistant
`)
	var manifestPath string
	var calls []string
	runner := CommandRunnerFunc(func(_ context.Context, _ string, name string, args []string, _, _ io.Writer) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if len(calls) == 1 {
			if name != "kubectl" || strings.Join(args[:2], " ") != "delete -f" {
				t.Fatalf("first command = %s %v, want kubectl delete -f", name, args)
			}
			manifestPath = args[2]
			data, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatalf("ReadFile teardown manifest: %v", err)
			}
			text := string(data)
			for _, want := range []string{
				"kind: Deployment",
				"  name: support-bot",
			} {
				if !strings.Contains(text, want) {
					t.Fatalf("teardown manifest missing %q:\n%s", want, text)
				}
			}
			if strings.Contains(text, "kind: Secret") || strings.Contains(text, "support-bot-env") {
				t.Fatalf("teardown manifest should not delete external secret:\n%s", text)
			}
			for _, runtime := range []string{"kind: StatefulSet", "  name: mysql", "  name: nats", "data-mysql-0", "data-nats-0"} {
				if strings.Contains(text, runtime) {
					t.Fatalf("teardown manifest should not delete shared runtime resource %q:\n%s", runtime, text)
				}
			}
			if strings.Contains(text, "coder-registry") {
				t.Fatalf("teardown manifest should not include shared namespace registry:\n%s", text)
			}
		}
		return nil
	})

	result, err := UndeployKubernetes(context.Background(), KubernetesUndeployOptions{
		AppDir:    app,
		Namespace: "ai-bots",
		Volumes:   true,
		Runner:    runner,
	})
	if err != nil {
		t.Fatalf("UndeployKubernetes: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %#v, want only manifest delete", calls)
	}
	if manifestPath == "" {
		t.Fatalf("manifest path was not captured")
	}
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("teardown manifest was not removed: %v", err)
	}
	if len(result.Commands) != 1 {
		t.Fatalf("commands = %#v, want one app teardown command", result.Commands)
	}
}
