package actuator

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func actuatorScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	return s
}

func nn(ns, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: ns, Name: name}
}

func makeDeployment(name, ns string, selector map[string]string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "openjdk:17"}},
				},
			},
		},
	}
}

func makeStatefulSet(name, ns string, selector map[string]string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app"}},
				},
			},
		},
	}
}

func makeDaemonSet(name, ns string, selector map[string]string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app"}},
				},
			},
		},
	}
}

// --- setRestartAnnotation ---

func TestSetRestartAnnotation_SetsAnnotation(t *testing.T) {
	m := map[string]string{}
	setRestartAnnotation(&m, "2026-01-01T00:00:00Z")
	if v := m["kubectl.kubernetes.io/restartedAt"]; v != "2026-01-01T00:00:00Z" {
		t.Errorf("unexpected annotation value: %q", v)
	}
}

func TestSetRestartAnnotation_NilMap_InitializesMap(t *testing.T) {
	var m map[string]string
	setRestartAnnotation(&m, "ts")
	if m == nil {
		t.Fatal("expected map to be initialized")
	}
	if m["kubectl.kubernetes.io/restartedAt"] != "ts" {
		t.Error("annotation not set on nil map")
	}
}

func TestSetRestartAnnotation_EmptyValue_NoOp(t *testing.T) {
	var m map[string]string
	setRestartAnnotation(&m, "")
	if m != nil {
		t.Error("empty value should be a no-op; map should remain nil")
	}
}

func TestSetRestartAnnotation_PreservesExistingAnnotations(t *testing.T) {
	m := map[string]string{"existing": "value"}
	setRestartAnnotation(&m, "ts")
	if m["existing"] != "value" {
		t.Error("existing annotation was overwritten")
	}
}

// --- workloadSelector ---

func TestWorkloadSelector_Deployment_ReturnsSelector(t *testing.T) {
	labels := map[string]string{"app": "myapp"}
	dep := makeDeployment("myapp", "default", labels)
	c := fake.NewClientBuilder().WithScheme(actuatorScheme()).WithObjects(dep).Build()

	got, err := workloadSelector(context.Background(), c, ApplyInput{Kind: "Deployment", Name: "myapp", Namespace: "default"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["app"] != "myapp" {
		t.Errorf("unexpected selector: %v", got)
	}
}

func TestWorkloadSelector_StatefulSet_ReturnsSelector(t *testing.T) {
	labels := map[string]string{"app": "myss"}
	ss := makeStatefulSet("myss", "default", labels)
	c := fake.NewClientBuilder().WithScheme(actuatorScheme()).WithObjects(ss).Build()

	got, err := workloadSelector(context.Background(), c, ApplyInput{Kind: "StatefulSet", Name: "myss", Namespace: "default"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["app"] != "myss" {
		t.Errorf("unexpected selector: %v", got)
	}
}

func TestWorkloadSelector_DaemonSet_ReturnsSelector(t *testing.T) {
	labels := map[string]string{"app": "myds"}
	ds := makeDaemonSet("myds", "default", labels)
	c := fake.NewClientBuilder().WithScheme(actuatorScheme()).WithObjects(ds).Build()

	got, err := workloadSelector(context.Background(), c, ApplyInput{Kind: "DaemonSet", Name: "myds", Namespace: "default"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["app"] != "myds" {
		t.Errorf("unexpected selector: %v", got)
	}
}

func TestWorkloadSelector_UnsupportedKind_ReturnsError(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(actuatorScheme()).Build()
	_, err := workloadSelector(context.Background(), c, ApplyInput{Kind: "CronJob", Name: "x", Namespace: "default"})
	if err == nil {
		t.Error("expected error for unsupported kind")
	}
}

func TestWorkloadSelector_NotFound_ReturnsError(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(actuatorScheme()).Build()
	_, err := workloadSelector(context.Background(), c, ApplyInput{Kind: "Deployment", Name: "missing", Namespace: "default"})
	if err == nil {
		t.Error("expected error when deployment not found")
	}
}

// --- patchWorkload ---

func TestPatchWorkload_Deployment_SetsRestartAnnotation(t *testing.T) {
	dep := makeDeployment("myapp", "default", map[string]string{"app": "myapp"})
	c := fake.NewClientBuilder().WithScheme(actuatorScheme()).WithObjects(dep).Build()

	err := patchWorkload(context.Background(), c, ApplyInput{Kind: "Deployment", Name: "myapp", Namespace: "default"}, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &appsv1.Deployment{}
	_ = c.Get(context.Background(), nn("default", "myapp"), updated)
	ann := updated.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"]
	if ann != "2026-01-01T00:00:00Z" {
		t.Errorf("unexpected annotation: %q", ann)
	}
}

func TestPatchWorkload_StatefulSet_SetsRestartAnnotation(t *testing.T) {
	ss := makeStatefulSet("myss", "default", map[string]string{"app": "myss"})
	c := fake.NewClientBuilder().WithScheme(actuatorScheme()).WithObjects(ss).Build()

	err := patchWorkload(context.Background(), c, ApplyInput{Kind: "StatefulSet", Name: "myss", Namespace: "default"}, "ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &appsv1.StatefulSet{}
	_ = c.Get(context.Background(), nn("default", "myss"), updated)
	if updated.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] != "ts" {
		t.Error("restart annotation not set on StatefulSet")
	}
}

func TestPatchWorkload_DaemonSet_SetsRestartAnnotation(t *testing.T) {
	ds := makeDaemonSet("myds", "default", map[string]string{"app": "myds"})
	c := fake.NewClientBuilder().WithScheme(actuatorScheme()).WithObjects(ds).Build()

	err := patchWorkload(context.Background(), c, ApplyInput{Kind: "DaemonSet", Name: "myds", Namespace: "default"}, "ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &appsv1.DaemonSet{}
	_ = c.Get(context.Background(), nn("default", "myds"), updated)
	if updated.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] != "ts" {
		t.Error("restart annotation not set on DaemonSet")
	}
}

func TestPatchWorkload_UnsupportedKind_ReturnsError(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(actuatorScheme()).Build()
	err := patchWorkload(context.Background(), c, ApplyInput{Kind: "Job", Name: "x", Namespace: "default"}, "ts")
	if err == nil {
		t.Error("expected error for unsupported kind")
	}
}

// --- InPlaceActuator ---

func TestInPlaceActuator_PatchesRunningPods(t *testing.T) {
	labels := map[string]string{"app": "myapp"}
	dep := makeDeployment("myapp", "default", labels)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "myapp-pod", Namespace: "default",
			Labels: labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "app",
					Image: "openjdk:17",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("100m"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	c := fake.NewClientBuilder().WithScheme(actuatorScheme()).
		WithObjects(dep, pod).
		WithStatusSubresource(&corev1.Pod{}).
		Build()
	a := NewInPlaceActuator(c)

	newCPU := resource.MustParse("500m")
	err := a.Apply(context.Background(), ApplyInput{
		Kind:      "Deployment",
		Name:      "myapp",
		Namespace: "default",
		Containers: []ContainerPatch{{
			Name: "app",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: newCPU},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}
}

func TestInPlaceActuator_SkipsNonRunningPods(t *testing.T) {
	labels := map[string]string{"app": "myapp"}
	dep := makeDeployment("myapp", "default", labels)
	pendingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "myapp-pending", Namespace: "default",
			Labels: labels,
		},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}

	c := fake.NewClientBuilder().WithScheme(actuatorScheme()).WithObjects(dep, pendingPod).Build()
	a := NewInPlaceActuator(c)

	err := a.Apply(context.Background(), ApplyInput{
		Kind:      "Deployment",
		Name:      "myapp",
		Namespace: "default",
		Containers: []ContainerPatch{{
			Name:      "app",
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")}},
		}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInPlaceActuator_UnsupportedKind_ReturnsError(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(actuatorScheme()).Build()
	a := NewInPlaceActuator(c)
	err := a.Apply(context.Background(), ApplyInput{Kind: "CronJob", Name: "x", Namespace: "default"})
	if err == nil {
		t.Error("expected error for unsupported kind")
	}
}

// --- RestartActuator ---

func TestRestartActuator_SetsRestartAnnotationOnDeployment(t *testing.T) {
	dep := makeDeployment("myapp", "default", map[string]string{"app": "myapp"})
	c := fake.NewClientBuilder().WithScheme(actuatorScheme()).WithObjects(dep).Build()
	a := NewRestartActuator(c)

	err := a.Apply(context.Background(), ApplyInput{Kind: "Deployment", Name: "myapp", Namespace: "default"})
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}

	updated := &appsv1.Deployment{}
	_ = c.Get(context.Background(), nn("default", "myapp"), updated)
	if updated.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] == "" {
		t.Error("restartedAt annotation not set")
	}
}
