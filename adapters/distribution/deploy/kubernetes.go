package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	distlocal "github.com/fluxplane/engine/adapters/distribution/local"
	"github.com/fluxplane/engine/orchestration/distribution"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"sigs.k8s.io/yaml"
)

func kubernetesClientFor(runner CommandRunner, explicit KubernetesClient) KubernetesClient {
	if explicit != nil {
		return explicit
	}
	if runner != nil {
		return commandKubernetesClient{runner: runner}
	}
	return nativeKubernetesClient{}
}

type commandKubernetesClient struct {
	runner CommandRunner
}

type nativeKubernetesClient struct {
	dynamic dynamic.Interface
	typed   kubernetes.Interface
}

func decodeKubernetesObjects(content string) []*unstructured.Unstructured {
	decoder := k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(content), 4096)
	var out []*unstructured.Unstructured
	for {
		var raw map[string]any
		err := decoder.Decode(&raw)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || len(raw) == 0 {
			continue
		}
		out = append(out, &unstructured.Unstructured{Object: raw})
	}
	return out
}

func kubernetesObjectResource(obj *unstructured.Unstructured) (schema.GroupVersionResource, bool, error) {
	switch obj.GetAPIVersion() + "/" + obj.GetKind() {
	case "v1/Namespace":
		return schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, false, nil
	case "v1/Secret":
		return schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, true, nil
	case "v1/Service":
		return schema.GroupVersionResource{Version: "v1", Resource: "services"}, true, nil
	case "v1/PersistentVolumeClaim":
		return schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}, true, nil
	case "v1/ServiceAccount":
		return schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}, true, nil
	case "apps/v1/Deployment":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, true, nil
	case "apps/v1/StatefulSet":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, true, nil
	case "rbac.authorization.k8s.io/v1/Role":
		return schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}, true, nil
	case "rbac.authorization.k8s.io/v1/RoleBinding":
		return schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}, true, nil
	case "rbac.authorization.k8s.io/v1/ClusterRole":
		return schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}, false, nil
	case "rbac.authorization.k8s.io/v1/ClusterRoleBinding":
		return schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}, false, nil
	default:
		return schema.GroupVersionResource{}, false, fmt.Errorf("distribution deploy: unsupported kubernetes object %s/%s", obj.GetAPIVersion(), obj.GetKind())
	}
}

// KubernetesOptions configures kubectl-manifest deployment.
type KubernetesOptions struct {
	AppDir             string
	Profile            string
	Profiles           []string
	TempDir            string
	Image              string
	ImagePullPolicy    string
	BaseImage          string
	AuthPath           string
	AllowPluginAuthEnv bool
	Provider           string
	Model              string
	Effort             string
	Namespace          string
	NodeSelectors      []string
	RegistryMode       string
	Registry           string
	DryRun             bool
	Force              bool
	Out                io.Writer
	Err                io.Writer
	Runner             CommandRunner
	PortForwarder      PortForwarder
	dockerClient       DockerClient
	kubernetesClient   KubernetesClient
}

// KubernetesResult describes generated Kubernetes deployment artifacts.
type KubernetesResult struct {
	BaseImage  BaseImageResult
	AppBuild   AppBuildResult
	Manifest   string
	Namespace  string
	Image      string
	Registry   string
	Commands   [][]string
	DryRun     bool
	SecretName string
}

// KubernetesUndeployOptions configures kubectl-manifest teardown.
type KubernetesUndeployOptions struct {
	AppDir           string
	Namespace        string
	DryRun           bool
	Volumes          bool
	Out              io.Writer
	Err              io.Writer
	Runner           CommandRunner
	kubernetesClient KubernetesClient
}

// KubernetesUndeployResult describes generated Kubernetes teardown steps.
type KubernetesUndeployResult struct {
	AppDir    string
	Name      string
	Namespace string
	Commands  [][]string
	DryRun    bool
	Volumes   bool
}

// KubernetesManifestOptions configures plain Kubernetes manifest generation.
type KubernetesManifestOptions struct {
	AppDir          string
	Profile         string
	Profiles        []string
	Image           string
	ImagePullPolicy string
	EnvSecretName   string
	AuthPath        string
	Provider        string
	Model           string
	Effort          string
	Namespace       string
	NodeSelectors   []string
	RegistryMode    string
	Registry        string
	DryRun          bool
	Out             io.Writer
}

// KubernetesManifestResult describes generated Kubernetes manifest content.
type KubernetesManifestResult struct {
	AppDir     string
	Name       string
	Namespace  string
	Image      string
	SecretName string
	Content    string
	DryRun     bool
}

// DeployKubernetes builds local app artifacts, pushes the app image according to
// the registry mode, and applies generated kubectl manifests.
func DeployKubernetes(ctx context.Context, opts KubernetesOptions) (KubernetesResult, error) {
	baseImage := strings.TrimSpace(opts.BaseImage)
	if baseImage == "" {
		baseImage = defaultBaseImage
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	errOut := opts.Err
	if errOut == nil {
		errOut = io.Discard
	}
	runner := opts.Runner
	if runner == nil {
		runner = execRunner{}
	}
	dockerClient := dockerClientFor(opts.Runner, opts.dockerClient)
	kubernetesClient := kubernetesClientFor(opts.Runner, opts.kubernetesClient)
	loaded, err := distlocal.LoadWithOptions(ctx, firstNonEmpty(strings.TrimSpace(opts.AppDir), "."), distlocal.LoadOptions{Profile: opts.Profile, Profiles: opts.Profiles})
	if err != nil {
		return KubernetesResult{}, err
	}
	name := distributionName(loaded.Distribution.Spec)
	namespace := kubernetesName(firstNonEmpty(strings.TrimSpace(opts.Namespace), name))
	registryMode := strings.ToLower(firstNonEmpty(strings.TrimSpace(opts.RegistryMode), "auto"))
	if registryMode != "auto" && registryMode != "namespace" && registryMode != "k3d" && registryMode != "external" {
		return KubernetesResult{}, fmt.Errorf("distribution deploy: unsupported kubernetes registry mode %q", opts.RegistryMode)
	}
	tags := resolveAppBuildTags(loaded.Distribution.Spec, AppBuildOptions{Image: opts.Image})
	sourceImage := firstTag(tags)
	if sourceImage == "" {
		sourceImage = defaultAppImage
	}
	k3dCluster := "<current-k3d-cluster>"
	if registryMode == "auto" && !opts.DryRun {
		contextName, err := detectKubernetesContext(ctx)
		if err != nil {
			return KubernetesResult{}, err
		}
		if cluster, ok := strings.CutPrefix(contextName, "k3d-"); ok && cluster != "" {
			registryMode = "k3d"
			k3dCluster = cluster
		} else {
			registryMode = "namespace"
		}
	}
	if registryMode == "auto" {
		registryMode = "namespace"
	}
	if registryMode == "k3d" && !opts.DryRun && k3dCluster == "<current-k3d-cluster>" {
		var err error
		k3dCluster, err = detectK3DClusterName(ctx)
		if err != nil {
			return KubernetesResult{}, err
		}
	}
	refs := kubernetesImageRefs(namespace, name, sourceImage, registryMode, opts.Registry)
	appRuntime := resolveAppRuntime(loaded, appRuntimeOptions{
		Provider:           opts.Provider,
		Model:              opts.Model,
		Effort:             opts.Effort,
		AllowPluginAuthEnv: opts.AllowPluginAuthEnv,
	})

	app, err := BuildApp(ctx, AppBuildOptions{
		AppDir:             loaded.Root,
		Profile:            opts.Profile,
		Profiles:           opts.Profiles,
		Targets:            []string{"dockerfile", "docker-image"},
		Image:              sourceImage,
		DryRun:             opts.DryRun,
		Force:              opts.Force,
		BaseImage:          baseImage,
		AuthPath:           opts.AuthPath,
		AllowPluginAuthEnv: opts.AllowPluginAuthEnv,
		Provider:           opts.Provider,
		Model:              opts.Model,
		Effort:             opts.Effort,
		Out:                out,
		Err:                errOut,
		Runner:             opts.Runner,
		dockerClient:       opts.dockerClient,
	})
	if err != nil {
		return KubernetesResult{}, err
	}
	manifest := kubernetesManifestPath(loaded.Root, app.OutDir, "")
	rendered, err := kubernetesContent(loaded, kubernetesRenderOptions{
		Name:            name,
		Namespace:       namespace,
		Image:           refs.Cluster,
		ImagePullPolicy: opts.ImagePullPolicy,
		EnvSecretName:   "",
		AuthPath:        opts.AuthPath,
		AppRuntime:      appRuntime,
		NodeSelectors:   opts.NodeSelectors,
		IncludeRegistry: registryMode == "namespace",
	})
	if err != nil {
		return KubernetesResult{}, err
	}
	if err := maybeWriteKubernetesManifest(loaded.Root, manifest, rendered.Content, opts.DryRun, opts.Force, out); err != nil {
		return KubernetesResult{}, err
	}

	result := KubernetesResult{
		BaseImage:  BaseImageResult{Tags: []string{baseImage}},
		AppBuild:   app,
		Manifest:   manifest,
		Namespace:  namespace,
		Image:      refs.Cluster,
		Registry:   refs.Registry,
		DryRun:     opts.DryRun,
		SecretName: rendered.SecretName,
	}
	if rendered.SecretName != "" {
		printKubernetesSecretSummary(out, rendered.SecretName, rendered.SecretKeys, opts.DryRun)
	}

	if registryMode == "k3d" {
		command := []string{"k3d", "image", "import", sourceImage, "--cluster", k3dCluster}
		result.Commands = append(result.Commands, command)
		if opts.DryRun {
			_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(command, " "))
		} else if err := runner.Run(ctx, "", command[0], command[1:], out, errOut); err != nil {
			return KubernetesResult{}, err
		}
	}

	if registryMode == "namespace" {
		registryApply := []string{"kubectl", "apply", "-f", "<registry-manifest>"}
		result.Commands = append(result.Commands, registryApply)
		if opts.DryRun {
			_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(registryApply, " "))
		} else if err := kubernetesClient.ApplyManifest(ctx, rendered.RegistryContent, out, errOut); err != nil {
			return KubernetesResult{}, err
		}
		wait := []string{"kubectl", "rollout", "status", "deployment/coder-registry", "-n", namespace, "--timeout=120s"}
		result.Commands = append(result.Commands, wait)
		if opts.DryRun {
			_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(wait, " "))
		} else if err := kubernetesClient.WaitDeployment(ctx, namespace, "coder-registry", 120*time.Second, out, errOut); err != nil {
			return KubernetesResult{}, err
		}
		portForward := kubernetesRegistryPortForwardCommand(namespace)
		result.Commands = append(result.Commands, portForward)
		var forward PortForward
		if opts.DryRun {
			_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(portForward, " "))
		} else {
			forwarder := opts.PortForwarder
			if forwarder == nil {
				forwarder = kubectlPortForwarder{}
			}
			forward, err = forwarder.Forward(ctx, namespace, out, errOut)
			if err != nil {
				return KubernetesResult{}, err
			}
			defer func() { _ = forward.Close() }()
		}
		pushCommands := kubernetesPushCommands(sourceImage, refs.Push)
		for _, command := range pushCommands {
			result.Commands = append(result.Commands, command)
			if opts.DryRun {
				_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(command, " "))
				continue
			}
			if err := runDockerPushCommand(ctx, dockerClient, command, out, errOut); err != nil {
				return KubernetesResult{}, err
			}
		}
	}

	if registryMode == "external" {
		pushCommands := kubernetesPushCommands(sourceImage, refs.Push)
		for _, command := range pushCommands {
			result.Commands = append(result.Commands, command)
			if opts.DryRun {
				_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(command, " "))
				continue
			}
			if err := runDockerPushCommand(ctx, dockerClient, command, out, errOut); err != nil {
				return KubernetesResult{}, err
			}
		}
	}
	apply := []string{"kubectl", "apply", "-f", manifest}
	result.Commands = append(result.Commands, apply)
	if opts.DryRun {
		_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(apply, " "))
		return result, nil
	}
	if opts.Runner != nil && opts.kubernetesClient == nil {
		if err := runner.Run(ctx, "", apply[0], apply[1:], out, errOut); err != nil {
			return KubernetesResult{}, err
		}
		return result, nil
	}
	if err := kubernetesClient.ApplyManifest(ctx, rendered.Content, out, errOut); err != nil {
		return KubernetesResult{}, err
	}
	return result, nil
}

type kubectlPortForwarder struct{}

func kubernetesRegistryPortForwardCommand(namespace string) []string {
	return []string{"kubectl", "port-forward", "-n", namespace, "--address", "127.0.0.1", "service/coder-registry", defaultRegistryPort + ":" + defaultRegistryPort}
}

type kubectlPortForward struct {
	stop chan struct{}
	done chan error
}

func waitForPortForward(ctx context.Context, ready <-chan struct{}, done <-chan error, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case <-ready:
			return nil
		case <-ctx.Done():
			return fmt.Errorf("distribution deploy: wait for registry port-forward: %w", ctx.Err())
		case err := <-done:
			if err != nil {
				return fmt.Errorf("distribution deploy: registry port-forward exited before it was ready: %w", err)
			}
			return fmt.Errorf("distribution deploy: registry port-forward exited before it was ready")
		case <-deadline.C:
			return fmt.Errorf("distribution deploy: timed out waiting for registry port-forward")
		}
	}
}

// UndeployKubernetes deletes generated Kubernetes app resources.
func UndeployKubernetes(ctx context.Context, opts KubernetesUndeployOptions) (KubernetesUndeployResult, error) {
	appDir := strings.TrimSpace(opts.AppDir)
	if appDir == "" {
		appDir = "."
	}
	loaded, err := distlocal.Load(ctx, appDir)
	if err != nil {
		return KubernetesUndeployResult{}, err
	}
	name := distributionName(loaded.Distribution.Spec)
	namespace := kubernetesName(firstNonEmpty(strings.TrimSpace(opts.Namespace), name))
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	errOut := opts.Err
	if errOut == nil {
		errOut = io.Discard
	}
	runner := opts.Runner
	if runner == nil {
		runner = execRunner{}
	}
	kubernetesClient := kubernetesClientFor(opts.Runner, opts.kubernetesClient)
	content := kubernetesTeardownContent(loaded, kubernetesTeardownOptions{
		Name:      name,
		Namespace: namespace,
	})
	result := KubernetesUndeployResult{
		AppDir:    loaded.Root,
		Name:      name,
		Namespace: namespace,
		DryRun:    opts.DryRun,
		Volumes:   opts.Volumes,
	}
	deleteCommand := []string{"kubectl", "delete", "-f", "<kubernetes-teardown-manifest>", "--ignore-not-found"}
	if !opts.DryRun {
		deleteCommand = []string{"kubectl", "delete", "-f", "<native-kubernetes-client>", "--ignore-not-found"}
	}
	result.Commands = append(result.Commands, deleteCommand)
	if opts.DryRun {
		_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(deleteCommand, " "))
	} else if err := kubernetesClient.DeleteManifest(ctx, content, out, errOut); err != nil {
		return KubernetesUndeployResult{}, err
	}
	if opts.Volumes {
		pvcs := kubernetesTeardownPVCs(loaded)
		if len(pvcs) > 0 {
			volumeCommand := append([]string{"kubectl", "delete", "pvc"}, pvcs...)
			volumeCommand = append(volumeCommand, "-n", namespace, "--ignore-not-found")
			result.Commands = append(result.Commands, volumeCommand)
			if opts.DryRun {
				_, _ = fmt.Fprintf(out, "command=%s\n", strings.Join(volumeCommand, " "))
			} else if opts.Runner != nil && opts.kubernetesClient == nil {
				if err := runner.Run(ctx, "", volumeCommand[0], volumeCommand[1:], out, errOut); err != nil {
					return KubernetesUndeployResult{}, err
				}
			} else if err := kubernetesClient.DeleteManifest(ctx, kubernetesPVCIdentities(namespace, pvcs), out, errOut); err != nil {
				return KubernetesUndeployResult{}, err
			}
		}
	}
	return result, nil
}

// GenerateKubernetesManifests generates plain kubectl manifests for an app image.
func GenerateKubernetesManifests(ctx context.Context, opts KubernetesManifestOptions) (KubernetesManifestResult, error) {
	appDir := strings.TrimSpace(opts.AppDir)
	if appDir == "" {
		appDir = "."
	}
	loaded, err := distlocal.LoadWithOptions(ctx, appDir, distlocal.LoadOptions{Profile: opts.Profile, Profiles: opts.Profiles})
	if err != nil {
		return KubernetesManifestResult{}, err
	}
	name := distributionName(loaded.Distribution.Spec)
	namespace := kubernetesName(firstNonEmpty(strings.TrimSpace(opts.Namespace), name))
	image := strings.TrimSpace(opts.Image)
	if image == "" {
		image = firstTag(resolveTags(loaded.Distribution.Spec, nil))
	}
	if image == "" {
		image = defaultAppImage
	}
	appRuntime := resolveAppRuntime(loaded, appRuntimeOptions{
		Provider: opts.Provider,
		Model:    opts.Model,
		Effort:   opts.Effort,
	})
	rendered, err := kubernetesContent(loaded, kubernetesRenderOptions{
		Name:            name,
		Namespace:       namespace,
		Image:           image,
		ImagePullPolicy: opts.ImagePullPolicy,
		EnvSecretName:   opts.EnvSecretName,
		AuthPath:        opts.AuthPath,
		AppRuntime:      appRuntime,
		NodeSelectors:   opts.NodeSelectors,
		IncludeRegistry: false,
	})
	if err != nil {
		return KubernetesManifestResult{}, err
	}
	result := KubernetesManifestResult{
		AppDir:     loaded.Root,
		Name:       name,
		Namespace:  namespace,
		Image:      image,
		SecretName: rendered.SecretName,
		Content:    rendered.Content,
		DryRun:     opts.DryRun,
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	if opts.DryRun {
		_, _ = io.WriteString(out, rendered.Content)
	}
	return result, nil
}

func kubernetesManifestPath(appRoot, outDir, explicitOutDir string) string {
	if strings.TrimSpace(explicitOutDir) != "" {
		return filepath.Join(outDir, "kubernetes.yaml")
	}
	return filepath.Join(appDeployDir(appRoot), "kubernetes.yaml")
}

type kubernetesRenderOptions struct {
	Name            string
	Namespace       string
	Image           string
	ImagePullPolicy string
	EnvSecretName   string
	AuthPath        string
	AppRuntime      appRuntimeOptions
	NodeSelectors   []string
	IncludeRegistry bool
	OmitNamespace   bool
}

type kubernetesRenderResult struct {
	Content         string
	RegistryContent string
	SecretName      string
	SecretKeys      []string
}

type kubernetesEnvSecret struct {
	Name string
}

type kubernetesImageRefSet struct {
	Registry string
	Push     string
	Cluster  string
}

type kubernetesRBACSpec struct {
	Enabled       bool
	AllNamespaces bool
	Namespaces    []string
}

type kubernetesTeardownOptions struct {
	Name      string
	Namespace string
}

func kubernetesContent(loaded distribution.Loaded, opts kubernetesRenderOptions) (kubernetesRenderResult, error) {
	name := kubernetesName(firstNonEmpty(opts.Name, loaded.Distribution.Spec.Name, "app"))
	namespace := kubernetesName(firstNonEmpty(opts.Namespace, name))
	image := strings.TrimSpace(opts.Image)
	if image == "" {
		image = defaultAppImage
	}
	secret, err := kubernetesEnvSecretForLoaded(loaded, name, opts.EnvSecretName)
	if err != nil {
		return kubernetesRenderResult{}, err
	}
	nodeSelectors, err := parseKubernetesNodeSelectors(opts.NodeSelectors)
	if err != nil {
		return kubernetesRenderResult{}, err
	}
	var registryContent string
	var docs []string
	if opts.IncludeRegistry {
		registryContent = kubernetesRegistryContent(namespace)
		docs = append(docs, splitYAMLDocuments(registryContent)...)
	} else if !opts.OmitNamespace {
		docs = append(docs, kubernetesNamespace(namespace))
	}
	if composeUsesMySQL(loaded.Launch) {
		docs = append(docs, splitYAMLDocuments(kubernetesMySQL(namespace))...)
	}
	if composeUsesNATS(loaded.Launch) {
		docs = append(docs, splitYAMLDocuments(kubernetesNATS(namespace))...)
	}
	rbac := kubernetesRBACSpecForLoaded(loaded, namespace)
	if rbac.Enabled {
		docs = append(docs, kubernetesServiceAccount(namespace, name))
		docs = append(docs, kubernetesRBAC(namespace, name, rbac)...)
	}
	docs = append(docs, kubernetesAppService(namespace, name))
	docs = append(docs, kubernetesAppDeployment(namespace, name, image, opts.ImagePullPolicy, opts.AuthPath, opts.AppRuntime, loaded.Launch, secret.Name, nodeSelectors, rbac.Enabled))
	content := joinYAMLDocuments(docs)
	return kubernetesRenderResult{
		Content:         content,
		RegistryContent: registryContent,
		SecretName:      secret.Name,
	}, nil
}

func kubernetesYAML(objects ...any) string {
	var docs []string
	for _, obj := range objects {
		if obj == nil {
			continue
		}
		raw, err := k8sruntime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			panic(fmt.Sprintf("convert kubernetes object: %v", err))
		}
		pruneKubernetesYAML(raw)
		data, err := yaml.Marshal(raw)
		if err != nil {
			panic(fmt.Sprintf("marshal kubernetes object: %v", err))
		}
		docs = append(docs, string(data))
	}
	return joinYAMLDocuments(docs)
}

func pruneKubernetesYAML(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		if metadata, ok := typed["metadata"].(map[string]any); ok {
			delete(metadata, "creationTimestamp")
			if len(metadata) == 0 {
				delete(typed, "metadata")
			}
		}
		delete(typed, "status")
		for key, child := range typed {
			if pruneKubernetesYAML(child) {
				delete(typed, key)
			}
		}
		return len(typed) == 0
	case []any:
		for _, child := range typed {
			pruneKubernetesYAML(child)
		}
		return len(typed) == 0
	default:
		return false
	}
}

func objectMeta(namespace, name string) metav1.ObjectMeta {
	meta := metav1.ObjectMeta{Name: name}
	if namespace != "" {
		meta.Namespace = namespace
	}
	return meta
}

func int32Ptr(value int32) *int32 {
	return &value
}

func quantity(value string) resource.Quantity {
	return resource.MustParse(value)
}

func stringMapEnv(values map[string]string) []corev1.EnvVar {
	keys := sortedKeys(values)
	out := make([]corev1.EnvVar, 0, len(keys))
	for _, key := range keys {
		out = append(out, corev1.EnvVar{Name: key, Value: values[key]})
	}
	return out
}

func kubernetesTeardownContent(loaded distribution.Loaded, opts kubernetesTeardownOptions) string {
	name := kubernetesName(firstNonEmpty(opts.Name, loaded.Distribution.Spec.Name, "app"))
	namespace := kubernetesName(firstNonEmpty(opts.Namespace, name))
	var docs []string
	if composeUsesMySQL(loaded.Launch) {
		docs = append(docs, kubernetesServiceIdentity(namespace, "mysql"))
		docs = append(docs, kubernetesStatefulSetIdentity(namespace, "mysql"))
	}
	if composeUsesNATS(loaded.Launch) {
		docs = append(docs, kubernetesServiceIdentity(namespace, "nats"))
		docs = append(docs, kubernetesStatefulSetIdentity(namespace, "nats"))
	}
	rbac := kubernetesRBACSpecForLoaded(loaded, namespace)
	if rbac.Enabled {
		docs = append(docs, kubernetesServiceAccountIdentity(namespace, name))
		docs = append(docs, kubernetesRBACIdentities(namespace, name, rbac)...)
	}
	docs = append(docs, kubernetesServiceIdentity(namespace, name))
	docs = append(docs, kubernetesDeploymentIdentity(namespace, name))
	return joinYAMLDocuments(docs)
}

func kubernetesTeardownPVCs(loaded distribution.Loaded) []string {
	var pvcs []string
	if composeUsesMySQL(loaded.Launch) {
		pvcs = append(pvcs, "data-mysql-0")
	}
	if composeUsesNATS(loaded.Launch) {
		pvcs = append(pvcs, "data-nats-0")
	}
	return pvcs
}

func kubernetesPVCIdentities(namespace string, names []string) string {
	var objects []any
	for _, name := range names {
		objects = append(objects, &corev1.PersistentVolumeClaim{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
			ObjectMeta: objectMeta(namespace, name),
		})
	}
	return kubernetesYAML(objects...)
}

func kubernetesRBACSpecForLoaded(loaded distribution.Loaded, fallbackNamespace string) kubernetesRBACSpec {
	var spec kubernetesRBACSpec
	for _, bundle := range loaded.Distribution.Bundles {
		for _, ref := range bundle.Plugins {
			if strings.TrimSpace(ref.Name) != "kubernetes" {
				continue
			}
			spec.Enabled = true
			if boolFromConfig(ref.Config["all_namespaces"]) {
				spec.AllNamespaces = true
			}
			spec.Namespaces = append(spec.Namespaces, namespacesFromConfig(ref.Config["namespaces"])...)
			spec.Namespaces = append(spec.Namespaces, namespacesFromConfig(ref.Config["namespace"])...)
		}
	}
	if !spec.Enabled {
		return spec
	}
	spec.Namespaces = normalizeKubernetesNamespaces(spec.Namespaces)
	if spec.AllNamespaces {
		spec.Namespaces = nil
		return spec
	}
	if len(spec.Namespaces) == 0 {
		spec.Namespaces = []string{kubernetesName(firstNonEmpty(strings.TrimSpace(fallbackNamespace), "default"))}
	}
	return spec
}

func boolFromConfig(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func namespacesFromConfig(value any) []string {
	switch v := value.(type) {
	case string:
		return splitKubernetesNamespaces(v)
	case []string:
		var out []string
		for _, item := range v {
			out = append(out, splitKubernetesNamespaces(item)...)
		}
		return out
	case []any:
		var out []string
		for _, item := range v {
			out = append(out, namespacesFromConfig(item)...)
		}
		return out
	default:
		return nil
	}
}

func splitKubernetesNamespaces(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t'
	})
}

func normalizeKubernetesNamespaces(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		namespace := strings.TrimSpace(value)
		if namespace == "" || seen[namespace] {
			continue
		}
		seen[namespace] = true
		out = append(out, namespace)
	}
	sort.Strings(out)
	return out
}

func kubernetesEnvSecretForLoaded(loaded distribution.Loaded, name, override string) (kubernetesEnvSecret, error) {
	for _, root := range loaded.Launch.Workspace.Roots {
		if len(cleanStrings(root.EnvFiles)) > 0 {
			return kubernetesEnvSecret{}, fmt.Errorf("distribution deploy: kubernetes target does not support env_files on workspace root %q", root.Name)
		}
	}
	patterns := cleanStrings(loaded.Launch.Workspace.EnvFiles)
	if len(patterns) == 0 {
		return kubernetesEnvSecret{}, nil
	}
	secretName := firstNonEmpty(override, name+"-env")
	if strings.Contains(secretName, "{{") {
		return kubernetesEnvSecret{Name: strings.TrimSpace(secretName)}, nil
	}
	return kubernetesEnvSecret{
		Name: kubernetesName(secretName),
	}, nil
}

func kubernetesNamespace(namespace string) string {
	return kubernetesYAML(&corev1.Namespace{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: objectMeta("", namespace),
	})
}

func kubernetesServiceAccount(namespace, name string) string {
	return kubernetesYAML(&corev1.ServiceAccount{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"},
		ObjectMeta: objectMeta(namespace, name),
	})
}

func kubernetesServiceAccountIdentity(namespace, name string) string {
	return kubernetesServiceAccount(namespace, name)
}

func kubernetesRBAC(namespace, name string, spec kubernetesRBACSpec) []string {
	roleName := kubernetesName(name + "-kubernetes-reader")
	if spec.AllNamespaces {
		return []string{
			kubernetesClusterRole(roleName, true),
			kubernetesClusterRoleBinding(roleName, namespace, name),
		}
	}
	var docs []string
	if len(spec.Namespaces) > 0 {
		docs = append(docs, kubernetesClusterRole(kubernetesName(name+"-kubernetes-namespaces-reader"), false))
		docs = append(docs, kubernetesClusterRoleBinding(kubernetesName(name+"-kubernetes-namespaces-reader"), namespace, name))
	}
	for _, targetNamespace := range spec.Namespaces {
		docs = append(docs, kubernetesNamespaceRole(targetNamespace, roleName))
		docs = append(docs, kubernetesNamespaceRoleBinding(targetNamespace, roleName, namespace, name))
	}
	return docs
}

func kubernetesRBACIdentities(namespace, name string, spec kubernetesRBACSpec) []string {
	roleName := kubernetesName(name + "-kubernetes-reader")
	if spec.AllNamespaces {
		return []string{
			kubernetesClusterRoleIdentity(roleName),
			kubernetesClusterRoleBindingIdentity(roleName),
		}
	}
	var docs []string
	if len(spec.Namespaces) > 0 {
		namespaceRole := kubernetesName(name + "-kubernetes-namespaces-reader")
		docs = append(docs, kubernetesClusterRoleIdentity(namespaceRole))
		docs = append(docs, kubernetesClusterRoleBindingIdentity(namespaceRole))
	}
	for _, targetNamespace := range spec.Namespaces {
		docs = append(docs, kubernetesRoleIdentity(targetNamespace, roleName))
		docs = append(docs, kubernetesRoleBindingIdentity(targetNamespace, roleName))
	}
	return docs
}

func kubernetesClusterRole(name string, allNamespaces bool) string {
	rules := []rbacv1.PolicyRule{}
	if allNamespaces {
		rules = append(rules, kubernetesReadRules()...)
	}
	rules = append(rules, rbacv1.PolicyRule{
		APIGroups: []string{""},
		Resources: []string{"namespaces"},
		Verbs:     []string{"get", "list", "watch"},
	})
	return kubernetesYAML(&rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole"},
		ObjectMeta: objectMeta("", name),
		Rules:      rules,
	})
}

func kubernetesClusterRoleBinding(name, serviceAccountNamespace, serviceAccountName string) string {
	return kubernetesYAML(&rbacv1.ClusterRoleBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding"},
		ObjectMeta: objectMeta("", name),
		Subjects: []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      serviceAccountName,
			Namespace: serviceAccountNamespace,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     name,
		},
	})
}

func kubernetesNamespaceRole(namespace, name string) string {
	return kubernetesYAML(&rbacv1.Role{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "Role"},
		ObjectMeta: objectMeta(namespace, name),
		Rules:      kubernetesReadRules(),
	})
}

func kubernetesNamespaceRoleBinding(namespace, name, serviceAccountNamespace, serviceAccountName string) string {
	return kubernetesYAML(&rbacv1.RoleBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "RoleBinding"},
		ObjectMeta: objectMeta(namespace, name),
		Subjects: []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      serviceAccountName,
			Namespace: serviceAccountNamespace,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     name,
		},
	})
}

func kubernetesReadRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{APIGroups: []string{""}, Resources: []string{"pods", "services"}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get", "list", "watch"}},
	}
}

func kubernetesRoleIdentity(namespace, name string) string {
	return kubernetesYAML(&rbacv1.Role{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "Role"},
		ObjectMeta: objectMeta(namespace, name),
	})
}

func kubernetesRoleBindingIdentity(namespace, name string) string {
	return kubernetesYAML(&rbacv1.RoleBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "RoleBinding"},
		ObjectMeta: objectMeta(namespace, name),
	})
}

func kubernetesClusterRoleIdentity(name string) string {
	return kubernetesYAML(&rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole"},
		ObjectMeta: objectMeta("", name),
	})
}

func kubernetesClusterRoleBindingIdentity(name string) string {
	return kubernetesYAML(&rbacv1.ClusterRoleBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding"},
		ObjectMeta: objectMeta("", name),
	})
}

func kubernetesAppService(namespace, name string) string {
	return kubernetesYAML(&corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: objectMeta(namespace, name),
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app.kubernetes.io/name": name},
			Ports: []corev1.ServicePort{{
				Name:       "control",
				Port:       18080,
				TargetPort: intstr.FromString("control"),
			}},
		},
	})
}

func kubernetesServiceIdentity(namespace, name string) string {
	return kubernetesYAML(&corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: objectMeta(namespace, name),
	})
}

func kubernetesDeploymentIdentity(namespace, name string) string {
	return kubernetesYAML(&appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: objectMeta(namespace, name),
	})
}

func kubernetesStatefulSetIdentity(namespace, name string) string {
	return kubernetesYAML(&appsv1.StatefulSet{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"},
		ObjectMeta: objectMeta(namespace, name),
	})
}

func kubernetesAppDeployment(namespace, name, image, imagePullPolicy, authPath string, appRuntime appRuntimeOptions, launch distribution.LaunchConfig, secretName string, nodeSelectors map[string]string, serviceAccount bool) string {
	env := kubernetesRuntimeEnv(launch)
	imagePullPolicy = firstNonEmpty(strings.TrimSpace(imagePullPolicy), "IfNotPresent")
	labels := map[string]string{"app.kubernetes.io/name": name}
	container := corev1.Container{
		Name:            "app",
		Image:           image,
		ImagePullPolicy: corev1.PullPolicy(imagePullPolicy),
		Args:            appServeCommandWithHealthAddr(authPath, appRuntime, defaultKubeHealthAddr),
		Ports:           []corev1.ContainerPort{{Name: "control", ContainerPort: 18080}},
		Env:             stringMapEnv(env),
		ReadinessProbe: &corev1.Probe{
			ProbeHandler:     corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: appHealthcheckCommand()}},
			PeriodSeconds:    10,
			FailureThreshold: 12,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler:     corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: appHealthcheckCommand()}},
			PeriodSeconds:    20,
			FailureThreshold: 6,
		},
	}
	if secretName != "" {
		container.EnvFrom = []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}}}}
	}
	podSpec := corev1.PodSpec{
		NodeSelector: nodeSelectors,
		Containers:   []corev1.Container{container},
	}
	if serviceAccount {
		podSpec.ServiceAccountName = name
	}
	return kubernetesYAML(&appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: objectMeta(namespace, name),
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       podSpec,
			},
		},
	})
}

func kubernetesRuntimeEnv(launch distribution.LaunchConfig) map[string]string {
	env := map[string]string{}
	if composeUsesMySQL(launch) {
		name := strings.TrimSpace(launch.Data.Store.DSNEnv)
		if name == "" {
			name = defaultMySQLDSNEnv
		}
		env[name] = "fluxplane:fluxplane@tcp(mysql:3306)/fluxplane?parseTime=true&multiStatements=true"
	}
	if composeUsesNATS(launch) {
		name := strings.TrimSpace(launch.Events.Store.DSNEnv)
		if name == "" {
			name = defaultNATSDSNEnv
		}
		env[name] = "nats://nats:4222"
	}
	return env
}

func kubernetesRegistryContent(namespace string) string {
	labels := map[string]string{"app.kubernetes.io/name": "coder-registry"}
	return kubernetesYAML(
		&corev1.Namespace{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"}, ObjectMeta: objectMeta("", namespace)},
		&corev1.PersistentVolumeClaim{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
			ObjectMeta: objectMeta(namespace, "coder-registry-data"),
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: quantity("5Gi")}},
			},
		},
		&appsv1.Deployment{
			TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
			ObjectMeta: objectMeta(namespace, "coder-registry"),
			Spec: appsv1.DeploymentSpec{
				Replicas: int32Ptr(1),
				Selector: &metav1.LabelSelector{MatchLabels: labels},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: labels},
					Spec: corev1.PodSpec{Containers: []corev1.Container{{
						Name:  "registry",
						Image: "registry:2",
						Ports: []corev1.ContainerPort{{Name: "registry", ContainerPort: 5000}},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "data",
							MountPath: "/var/lib/registry",
						}},
					}}, Volumes: []corev1.Volume{{
						Name:         "data",
						VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "coder-registry-data"}},
					}}},
				},
			},
		},
		&corev1.Service{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
			ObjectMeta: objectMeta(namespace, "coder-registry"),
			Spec: corev1.ServiceSpec{
				Selector: labels,
				Ports: []corev1.ServicePort{{
					Name:       "registry",
					Port:       5000,
					TargetPort: intstr.FromString("registry"),
				}},
			},
		},
	)
}

func kubernetesMySQL(namespace string) string {
	labels := map[string]string{"app.kubernetes.io/name": "mysql"}
	return kubernetesYAML(
		&corev1.Service{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
			ObjectMeta: objectMeta(namespace, "mysql"),
			Spec: corev1.ServiceSpec{
				Selector: labels,
				Ports: []corev1.ServicePort{{
					Name:       "mysql",
					Port:       3306,
					TargetPort: intstr.FromString("mysql"),
				}},
			},
		},
		&appsv1.StatefulSet{
			TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"},
			ObjectMeta: objectMeta(namespace, "mysql"),
			Spec: appsv1.StatefulSetSpec{
				ServiceName: "mysql",
				Replicas:    int32Ptr(1),
				Selector:    &metav1.LabelSelector{MatchLabels: labels},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: labels},
					Spec: corev1.PodSpec{Containers: []corev1.Container{{
						Name:  "mysql",
						Image: "mysql:8.4",
						Ports: []corev1.ContainerPort{{Name: "mysql", ContainerPort: 3306}},
						Env: []corev1.EnvVar{
							{Name: "MYSQL_DATABASE", Value: "fluxplane"},
							{Name: "MYSQL_USER", Value: "fluxplane"},
							{Name: "MYSQL_PASSWORD", Value: "fluxplane"},
							{Name: "MYSQL_ROOT_PASSWORD", Value: "fluxplane-root"},
						},
						VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/var/lib/mysql"}},
					}}},
				},
				VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
					ObjectMeta: metav1.ObjectMeta{Name: "data"},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: quantity("5Gi")}},
					},
				}},
			},
		},
	)
}

func kubernetesNATS(namespace string) string {
	labels := map[string]string{"app.kubernetes.io/name": "nats"}
	return kubernetesYAML(
		&corev1.Service{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
			ObjectMeta: objectMeta(namespace, "nats"),
			Spec: corev1.ServiceSpec{
				Selector: labels,
				Ports: []corev1.ServicePort{
					{Name: "client", Port: 4222, TargetPort: intstr.FromString("client")},
					{Name: "monitor", Port: 8222, TargetPort: intstr.FromString("monitor")},
				},
			},
		},
		&appsv1.StatefulSet{
			TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"},
			ObjectMeta: objectMeta(namespace, "nats"),
			Spec: appsv1.StatefulSetSpec{
				ServiceName: "nats",
				Replicas:    int32Ptr(1),
				Selector:    &metav1.LabelSelector{MatchLabels: labels},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: labels},
					Spec: corev1.PodSpec{Containers: []corev1.Container{{
						Name:         "nats",
						Image:        "nats:2.11-alpine",
						Args:         []string{"-js", "-sd", "/data", "-m", "8222"},
						Ports:        []corev1.ContainerPort{{Name: "client", ContainerPort: 4222}, {Name: "monitor", ContainerPort: 8222}},
						VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/data"}},
					}}},
				},
				VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
					ObjectMeta: metav1.ObjectMeta{Name: "data"},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: quantity("2Gi")}},
					},
				}},
			},
		},
	)
}

func kubernetesImageRefs(namespace, name, sourceImage, mode, registry string) kubernetesImageRefSet {
	repoTag := imageRepoTag(sourceImage, name)
	switch mode {
	case "external":
		registry = strings.Trim(strings.TrimSpace(registry), "/")
		if registry == "" {
			return kubernetesImageRefSet{Registry: imageRegistry(sourceImage), Push: sourceImage, Cluster: sourceImage}
		}
		cluster := registry + "/" + repoTag
		return kubernetesImageRefSet{Registry: registry, Push: cluster, Cluster: cluster}
	case "namespace":
		cluster := "coder-registry." + namespace + ".svc.cluster.local:" + defaultRegistryPort
		push := "127.0.0.1:" + defaultRegistryPort
		return kubernetesImageRefSet{Registry: cluster, Push: push + "/" + repoTag, Cluster: cluster + "/" + repoTag}
	default:
		return kubernetesImageRefSet{Registry: "k3d", Push: "", Cluster: sourceImage}
	}
}

func kubernetesPushCommands(sourceImage, pushImage string) [][]string {
	if strings.TrimSpace(sourceImage) == strings.TrimSpace(pushImage) {
		return [][]string{{"docker", "push", pushImage}}
	}
	return [][]string{
		{"docker", "tag", sourceImage, pushImage},
		{"docker", "push", pushImage},
	}
}

func imageRepoTag(image, fallback string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return kubernetesName(fallback) + ":latest"
	}
	parts := strings.Split(image, "/")
	if len(parts) > 1 && imageRegistry(image) != "" {
		parts = parts[1:]
	}
	repo := strings.Join(parts, "/")
	if strings.Contains(parts[len(parts)-1], ":") {
		return repo
	}
	return repo + ":latest"
}

func imageRegistry(image string) string {
	parts := strings.Split(strings.TrimSpace(image), "/")
	if len(parts) < 2 {
		return ""
	}
	first := parts[0]
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		return first
	}
	return ""
}

func kubernetesName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "app"
	}
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-")
	}
	if out == "" {
		return "app"
	}
	return out
}

func currentK3DClusterName(ctx context.Context) (string, error) {
	contextName, err := detectKubernetesContext(ctx)
	if err != nil {
		return "", err
	}
	cluster, ok := strings.CutPrefix(contextName, "k3d-")
	if !ok || cluster == "" {
		return "", fmt.Errorf("distribution deploy: registry-mode k3d requires a k3d current context; got %q", contextName)
	}
	return cluster, nil
}

func currentKubernetesContext(ctx context.Context) (string, error) {
	_ = ctx
	config, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	if err != nil {
		return "", fmt.Errorf("distribution deploy: resolve current kubernetes context: %w", err)
	}
	return strings.TrimSpace(config.CurrentContext), nil
}

func (c commandKubernetesClient) ApplyManifest(ctx context.Context, content string, stdout, stderr io.Writer) error {
	tempFile, err := writeTempManifest("coder-kubernetes-apply-*.yaml", content)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tempFile) }()
	return c.runner.Run(ctx, "", "kubectl", []string{"apply", "-f", tempFile}, stdout, stderr)
}

func (c commandKubernetesClient) DeleteManifest(ctx context.Context, content string, stdout, stderr io.Writer) error {
	tempFile, err := writeTempManifest("coder-kubernetes-delete-*.yaml", content)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tempFile) }()
	return c.runner.Run(ctx, "", "kubectl", []string{"delete", "-f", tempFile, "--ignore-not-found"}, stdout, stderr)
}

func (c commandKubernetesClient) WaitDeployment(ctx context.Context, namespace, name string, timeout time.Duration, stdout, stderr io.Writer) error {
	return c.runner.Run(ctx, "", "kubectl", []string{"rollout", "status", "deployment/" + name, "-n", namespace, "--timeout=" + strconv.Itoa(int(timeout.Seconds())) + "s"}, stdout, stderr)
}

func (c nativeKubernetesClient) clients() (dynamic.Interface, kubernetes.Interface, error) {
	if c.dynamic != nil && c.typed != nil {
		return c.dynamic, c.typed, nil
	}
	cfg, err := kubernetesRESTConfig()
	if err != nil {
		return nil, nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("distribution deploy: create kubernetes dynamic client: %w", err)
	}
	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("distribution deploy: create kubernetes client: %w", err)
	}
	return dyn, typed, nil
}

func (c nativeKubernetesClient) ApplyManifest(ctx context.Context, content string, stdout, stderr io.Writer) error {
	dyn, _, err := c.clients()
	if err != nil {
		return err
	}
	for _, obj := range decodeKubernetesObjects(content) {
		gvr, namespaced, err := kubernetesObjectResource(obj)
		if err != nil {
			return err
		}
		name := obj.GetName()
		if name == "" {
			return fmt.Errorf("distribution deploy: kubernetes %s/%s has no metadata.name", obj.GetAPIVersion(), obj.GetKind())
		}
		var resource dynamic.ResourceInterface
		if namespaced {
			resource = dyn.Resource(gvr).Namespace(obj.GetNamespace())
		} else {
			resource = dyn.Resource(gvr)
		}
		if _, err := resource.Apply(ctx, name, obj, metav1.ApplyOptions{FieldManager: "agentruntime-deploy", Force: true}); err != nil {
			return fmt.Errorf("distribution deploy: apply kubernetes %s/%s %s: %w", obj.GetAPIVersion(), obj.GetKind(), name, err)
		}
	}
	return nil
}

func (c nativeKubernetesClient) DeleteManifest(ctx context.Context, content string, stdout, stderr io.Writer) error {
	dyn, _, err := c.clients()
	if err != nil {
		return err
	}
	objects := decodeKubernetesObjects(content)
	for i := len(objects) - 1; i >= 0; i-- {
		obj := objects[i]
		gvr, namespaced, err := kubernetesObjectResource(obj)
		if err != nil {
			return err
		}
		name := obj.GetName()
		if name == "" {
			continue
		}
		var resource dynamic.ResourceInterface
		if namespaced {
			resource = dyn.Resource(gvr).Namespace(obj.GetNamespace())
		} else {
			resource = dyn.Resource(gvr)
		}
		if err := resource.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("distribution deploy: delete kubernetes %s/%s %s: %w", obj.GetAPIVersion(), obj.GetKind(), name, err)
		}
	}
	return nil
}

func (c nativeKubernetesClient) WaitDeployment(ctx context.Context, namespace, name string, timeout time.Duration, stdout, stderr io.Writer) error {
	_, typed, err := c.clients()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		deployment, err := typed.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("distribution deploy: get kubernetes deployment %s/%s: %w", namespace, name, err)
		}
		if deployment.Status.AvailableReplicas >= *deployment.Spec.Replicas || (deployment.Spec.Replicas == nil && deployment.Status.AvailableReplicas > 0) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("distribution deploy: wait for kubernetes deployment %s/%s: %w", namespace, name, ctx.Err())
		case <-ticker.C:
		}
	}
}

func kubernetesRESTConfig() (*rest.Config, error) {
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("distribution deploy: load kubernetes config: %w", err)
	}
	return cfg, nil
}

func (kubectlPortForwarder) Forward(ctx context.Context, namespace string, stdout, stderr io.Writer) (PortForward, error) {
	cfg, err := kubernetesRESTConfig()
	if err != nil {
		return nil, err
	}
	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("distribution deploy: create kubernetes client: %w", err)
	}
	service, err := typed.CoreV1().Services(namespace).Get(ctx, "coder-registry", metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("distribution deploy: get registry service: %w", err)
	}
	pods, err := typed.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(service.Spec.Selector).String()})
	if err != nil {
		return nil, fmt.Errorf("distribution deploy: list registry pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("distribution deploy: no registry pods found for service/coder-registry")
	}
	transport, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("distribution deploy: create kubernetes port-forward transport: %w", err)
	}
	url := typed.CoreV1().RESTClient().Post().Resource("pods").Namespace(namespace).Name(pods.Items[0].Name).SubResource("portforward").URL()
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, url)
	stop := make(chan struct{})
	ready := make(chan struct{})
	forwarder, err := portforward.New(dialer, []string{defaultRegistryPort + ":" + defaultRegistryPort}, stop, ready, stdout, stderr)
	if err != nil {
		return nil, fmt.Errorf("distribution deploy: create registry port-forward: %w", err)
	}
	forward := &kubectlPortForward{stop: stop, done: make(chan error, 1)}
	go func() {
		forward.done <- forwarder.ForwardPorts()
	}()
	if err := waitForPortForward(ctx, ready, forward.done, 10*time.Second); err != nil {
		_ = forward.Close()
		return nil, err
	}
	return forward, nil
}

func (f *kubectlPortForward) Close() error {
	if f == nil {
		return nil
	}
	close(f.stop)
	select {
	case err := <-f.done:
		return err
	case <-time.After(5 * time.Second):
		return fmt.Errorf("distribution deploy: registry port-forward did not stop")
	}
}
