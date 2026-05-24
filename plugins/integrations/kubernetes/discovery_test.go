package kubernetes

import (
	"context"
	"testing"

	corediscovery "github.com/fluxplane/fluxplane-core/core/discovery"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDiscoverEndpointCandidatesFindsLokiServiceAndPod(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "loki", Namespace: "monitoring", Labels: map[string]string{"app.kubernetes.io/name": "loki"}},
			Spec:       corev1.ServiceSpec{ClusterIP: "10.0.0.5", Ports: []corev1.ServicePort{{Name: "http-metrics", Port: 3100, TargetPort: intstr.FromInt(3100)}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "loki-0", Namespace: "monitoring", Labels: map[string]string{"name": "loki"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "loki", Ports: []corev1.ContainerPort{{Name: "http-metrics", ContainerPort: 3100}}}}},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.9"},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "promtail-metrics", Namespace: "monitoring", Labels: map[string]string{"app.kubernetes.io/name": "promtail"}},
			Spec:       corev1.ServiceSpec{ClusterIP: "10.0.0.8", Ports: []corev1.ServicePort{{Name: "http-metrics", Port: 3101}}},
		},
	)
	candidates, err := DiscoverEndpointCandidates(context.Background(), NewWithClient(nil, client), EndpointDiscoveryOptions{
		Product: "loki", Namespaces: []string{"monitoring"}, AllowPodIP: true,
	})
	if err != nil {
		t.Fatalf("DiscoverEndpointCandidates() error = %v", err)
	}
	if len(candidates) < 2 {
		t.Fatalf("candidates len = %d, want service and pod candidates: %#v", len(candidates), candidates)
	}
	for _, candidate := range candidates {
		if candidate.Source.Name == "promtail-metrics" {
			t.Fatalf("promtail candidate included: %#v", candidate)
		}
	}
	if candidates[0].Source.Name != "loki" || candidates[0].Port != 3100 {
		t.Fatalf("top candidate = %#v, want loki service on 3100", candidates[0])
	}
}

func TestDiscoverEndpointCandidatesSkipsPodsWhenPodIPDisabled(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "loki", Namespace: "monitoring", Labels: map[string]string{"app.kubernetes.io/name": "loki"}},
			Spec:       corev1.ServiceSpec{ClusterIP: "10.0.0.5", Ports: []corev1.ServicePort{{Name: "http-metrics", Port: 3100}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "loki-0", Namespace: "monitoring", Labels: map[string]string{"name": "loki"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "loki", Ports: []corev1.ContainerPort{{Name: "http-metrics", ContainerPort: 3100}}}}},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.9"},
		},
	)
	candidates, err := DiscoverEndpointCandidates(context.Background(), NewWithClient(nil, client), EndpointDiscoveryOptions{
		Product: "loki", Namespaces: []string{"monitoring"}, AllowPodIP: false,
	})
	if err != nil {
		t.Fatalf("DiscoverEndpointCandidates() error = %v", err)
	}
	for _, candidate := range candidates {
		if candidate.Source.Kind == "kubernetes.pod" {
			t.Fatalf("pod candidate included with AllowPodIP=false: %#v", candidate)
		}
	}
	if len(candidates) == 0 {
		t.Fatal("service candidates empty")
	}
}

func TestDiscoverEndpointCandidatesScansAllNamespacesAndEnv(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "latest", Labels: map[string]string{"app": "backend"}},
			Spec:       corev1.ServiceSpec{ClusterIP: "10.0.1.5", Ports: []corev1.ServicePort{{Name: "http", Port: 8080}}},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "grafana", Namespace: "monitoring", Labels: map[string]string{"app.kubernetes.io/name": "grafana"}},
			Spec:       corev1.ServiceSpec{ClusterIP: "10.0.2.5", Ports: []corev1.ServicePort{{Name: "http", Port: 3000}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "backend-abc", Namespace: "latest", Labels: map[string]string{"app": "backend"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name:  "backend",
				Ports: []corev1.ContainerPort{{Name: "grpc", ContainerPort: 50051}},
				Env: []corev1.EnvVar{{
					Name:  "DATABASE_URL",
					Value: "postgres://user:" + "redacted@" + "postgres.latest.svc:5432/app?sslmode=disable",
				}},
			}}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.2.0.5"},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "jobs"},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "worker",
				Env: []corev1.EnvVar{{
					Name:  "REDIS_URL",
					Value: "redis://:" + "redacted@" + "redis.jobs.svc:6379/0",
				}},
			}}}}},
		},
	)
	candidates, err := DiscoverEndpointCandidates(context.Background(), NewWithClient(nil, client), EndpointDiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverEndpointCandidates() error = %v", err)
	}
	assertCandidate(t, candidates, "kubernetes.service", "latest", "api", "http")
	assertCandidate(t, candidates, "kubernetes.service", "monitoring", "grafana", "grafana")
	postgres := assertCandidate(t, candidates, "kubernetes.pod.env", "latest", "backend-abc", "postgres")
	if postgres.URL != "postgres://postgres.latest.svc:5432/app" {
		t.Fatalf("postgres URL = %q, want sanitized URL without credentials/query", postgres.URL)
	}
	redis := assertCandidate(t, candidates, "kubernetes.deployment.env", "jobs", "worker", "redis")
	if redis.URL != "redis://redis.jobs.svc:6379/0" {
		t.Fatalf("redis URL = %q, want sanitized URL without credentials", redis.URL)
	}
}

func TestDiscoverEndpointCandidatesAttachesKubernetesSecretAuthRef(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "backend-abc", Namespace: "latest", Labels: map[string]string{"app": "backend"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "backend",
				Env: []corev1.EnvVar{{
					Name: "MYSQL_DSN",
					ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "backend-db"},
						Key:                  "dsn",
					}},
				}},
			}}},
		},
	)
	candidates, err := DiscoverEndpointCandidates(context.Background(), NewWithClient(nil, client), EndpointDiscoveryOptions{Product: "mysql", Namespaces: []string{"latest"}})
	if err != nil {
		t.Fatalf("DiscoverEndpointCandidates() error = %v", err)
	}
	mysql := assertCandidate(t, candidates, "kubernetes.pod.env", "latest", "backend-abc", "mysql")
	if mysql.URL != "" {
		t.Fatalf("secret-backed candidate URL = %q, want no secret material in endpoint URL", mysql.URL)
	}
	if want := coresecret.Kubernetes("latest", "backend-db", "dsn").ResourceName(); mysql.AuthRef != want {
		t.Fatalf("AuthRef = %q, want %q", mysql.AuthRef, want)
	}
}

func TestDiscoverEndpointCandidatesDatabaseProductIncludesMySQLService(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "mysql", Namespace: "latest"},
		Spec:       corev1.ServiceSpec{ClusterIP: "10.0.3.5", Ports: []corev1.ServicePort{{Name: "mysql", Port: 3306}}},
	})
	candidates, err := DiscoverEndpointCandidates(context.Background(), NewWithClient(nil, client), EndpointDiscoveryOptions{Product: "database", Namespaces: []string{"latest"}})
	if err != nil {
		t.Fatalf("DiscoverEndpointCandidates() error = %v", err)
	}
	mysql := assertCandidate(t, candidates, "kubernetes.service", "latest", "mysql", "mysql")
	if mysql.URL != "mysql://mysql.latest.svc:3306" {
		t.Fatalf("mysql service URL = %q, want mysql scheme", mysql.URL)
	}
}

func TestKubernetesSecretResolverResolvesSecretKey(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "backend-db", Namespace: "latest"},
		Data:       map[string][]byte{"dsn": []byte("mysql://user:pass@mysql.latest.svc:3306/app")},
	})
	plugin := NewWithClient(nil, client)
	plugin.cfg = Config{Namespaces: []string{"latest"}}
	material, ok, err := (kubernetesSecretResolver{plugin: plugin}).ResolveSecret(context.Background(), coresecret.Kubernetes("latest", "backend-db", "dsn"))
	if err != nil {
		t.Fatalf("ResolveSecret() error = %v", err)
	}
	if !ok || material.Value != "mysql://user:pass@mysql.latest.svc:3306/app" {
		t.Fatalf("ResolveSecret() = %#v, %v; want dsn", material, ok)
	}
}

func assertCandidate(t *testing.T, candidates []corediscovery.Candidate, kind, namespace, name, product string) corediscovery.Candidate {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.Source.Kind == kind && candidate.Source.Namespace == namespace && candidate.Source.Name == name && candidate.ProductHint == product {
			return candidate
		}
	}
	t.Fatalf("candidate kind=%s namespace=%s name=%s product=%s not found in %#v", kind, namespace, name, product, candidates)
	return corediscovery.Candidate{}
}
