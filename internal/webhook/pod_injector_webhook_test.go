package webhook

import (
	"context"
	"testing"
	"time"

	v1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// testScheme returns a scheme with all types needed for webhook tests.
func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = v1alpha1.AddToScheme(s)
	return s
}

// newInjector creates a PodInjector backed by a fake client pre-populated with objects.
func newInjector(objs ...runtime.Object) *PodInjector {
	c := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithRuntimeObjects(objs...).
		Build()
	return &PodInjector{Client: c, AgentImage: "ghcr.io/test/cairn-agent:latest"}
}

// standaloneNginxPod builds a pod with no ownerReferences (standalone).
func standaloneNginxPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "nginx-1", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "nginx", Image: "nginx:alpine"}},
		},
	}
}

// deploymentOwnedPod builds a pod that looks like it was created by a ReplicaSet
// which was created by a Deployment.
func deploymentOwnedPod() *corev1.Pod {
	trueVal := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-app-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "ReplicaSet",
				Name:       "my-app-rs",
				Controller: &trueVal,
			}},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "nginx:alpine"}},
		},
	}
}

func javaPodOwnedByDeployment(deployName string) *corev1.Pod {
	rsName := deployName + "-rs"
	trueVal := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployName + "-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "ReplicaSet",
				Name:       rsName,
				Controller: &trueVal,
			}},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "app",
				Image: "eclipse-temurin:21-jdk-alpine",
			}},
		},
	}
}

// replicaSet builds the RS that bridges the pod to the Deployment.
func replicaSet(rsName, deployName string) *appsv1.ReplicaSet {
	trueVal := true
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rsName,
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       deployName,
				Controller: &trueVal,
			}},
		},
	}
}

// rightsizePolicy builds a basic RightsizePolicy targeting a Deployment.
func rightsizePolicy() *v1alpha1.RightsizePolicy {
	return &v1alpha1.RightsizePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: "default"},
		Spec: v1alpha1.RightsizePolicySpec{
			CommonPolicySpec: v1alpha1.CommonPolicySpec{
				TargetRef: v1alpha1.TargetRef{Kind: "Deployment", Name: "my-app"},
				Mode:      v1alpha1.PolicyModeAuto,
			},
		},
	}
}

func javaRightsizePolicy(deployName string) *v1alpha1.RightsizePolicy {
	return &v1alpha1.RightsizePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: "default"},
		Spec: v1alpha1.RightsizePolicySpec{
			CommonPolicySpec: v1alpha1.CommonPolicySpec{
				TargetRef: v1alpha1.TargetRef{Kind: "Deployment", Name: deployName},
				Mode:      v1alpha1.PolicyModeAuto,
				Java: &v1alpha1.JavaPolicy{
					Enabled:     true,
					InjectAgent: true,
				},
			},
		},
	}
}

// rightsizeRecommendation builds a recommendation with CPU and memory set
// and DataReadySince in the past so the observation window check passes.
func rightsizeRecommendation(workloadName string) *v1alpha1.RightsizeRecommendation {
	cpuQ := resource.MustParse("200m")
	memQ := resource.MustParse("128Mi")
	recName := "deployment-" + workloadName
	ready := metav1.NewTime(time.Now().Add(-48 * time.Hour))
	return &v1alpha1.RightsizeRecommendation{
		ObjectMeta: metav1.ObjectMeta{Name: recName, Namespace: "default"},
		Spec: v1alpha1.RightsizeRecommendationSpec{
			TargetRef: v1alpha1.TargetRef{Kind: "Deployment", Name: workloadName},
		},
		Status: v1alpha1.RightsizeRecommendationStatus{
			DataReadySince: &ready,
			Containers: []v1alpha1.ContainerRecommendation{{
				ContainerName: "app",
				Recommended: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    cpuQ,
						corev1.ResourceMemory: memQ,
					},
				},
			}},
		},
	}
}

// --- tests ---

func TestDefault_StandalonePodd_NoMutation(t *testing.T) {
	p := newInjector()
	pod := standaloneNginxPod()
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	// No policy matches → no annotation set
	if pod.Annotations[containerTypeAnnotation] != "" {
		t.Errorf("standalone pod should not be annotated, got %q", pod.Annotations[containerTypeAnnotation])
	}
}

func TestDefault_NoPolicyForWorkload_NoMutation(t *testing.T) {
	const deployName = "my-app"
	rs := replicaSet(deployName+"-rs", deployName)
	pod := deploymentOwnedPod()
	p := newInjector(rs) // no policy
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if pod.Annotations[containerTypeAnnotation] != "" {
		t.Errorf("no policy → no annotation, got %q", pod.Annotations[containerTypeAnnotation])
	}
}

func TestDefault_NonJavaPod_MarkedStandard(t *testing.T) {
	const deployName = "my-app"
	rs := replicaSet(deployName+"-rs", deployName)
	policy := rightsizePolicy()
	pod := deploymentOwnedPod()
	p := newInjector(rs, policy)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if pod.Annotations[containerTypeAnnotation] != containerTypeStandard {
		t.Errorf("expected annotation=%s, got %q", containerTypeStandard, pod.Annotations[containerTypeAnnotation])
	}
}

func TestDefault_RecommendationApplied_OnPodCreation(t *testing.T) {
	const deployName = "my-app"
	rs := replicaSet(deployName+"-rs", deployName)
	policy := rightsizePolicy()
	rec := rightsizeRecommendation(deployName)
	pod := deploymentOwnedPod()
	p := newInjector(rs, policy, rec)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	cpu := pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
	if cpu.MilliValue() != 200 {
		t.Errorf("expected CPU=200m from recommendation, got %s", cpu.String())
	}
}

func TestDefault_SuspendedPolicy_NoMutation(t *testing.T) {
	const deployName = "my-app"
	rs := replicaSet(deployName+"-rs", deployName)
	policy := rightsizePolicy()
	policy.Spec.Suspended = true
	rec := rightsizeRecommendation(deployName)
	pod := deploymentOwnedPod()
	p := newInjector(rs, policy, rec)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	// Suspended = kill-switch: no annotation, no resource patch
	if pod.Annotations[containerTypeAnnotation] != "" {
		t.Errorf("suspended policy: expected no annotation, got %q", pod.Annotations[containerTypeAnnotation])
	}
	if pod.Spec.Containers[0].Resources.Requests != nil {
		t.Error("suspended policy: resources should not be patched")
	}
}

func TestDefault_JavaPod_AgentInjected(t *testing.T) {
	const deployName = "java-app"
	rs := replicaSet(deployName+"-rs", deployName)
	policy := javaRightsizePolicy(deployName)
	pod := javaPodOwnedByDeployment(deployName)
	p := newInjector(rs, policy)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	// Should be annotated as java and have the agent volume
	if pod.Annotations[containerTypeAnnotation] != containerTypeJava {
		t.Errorf("expected annotation=%s, got %q", containerTypeJava, pod.Annotations[containerTypeAnnotation])
	}
	if len(pod.Spec.Volumes) == 0 {
		t.Error("expected agent volume to be added")
	}
	jto := envValue(pod.Spec.Containers[0].Env)
	if jto == "" {
		t.Error("expected JAVA_TOOL_OPTIONS to be set with -javaagent")
	}
}

func TestDefault_AlreadyAnnotated_Skipped(t *testing.T) {
	const deployName = "my-app"
	rs := replicaSet(deployName+"-rs", deployName)
	policy := rightsizePolicy()
	pod := deploymentOwnedPod()
	pod.Annotations = map[string]string{
		containerTypeAnnotation: containerTypeStandard, // already processed
	}
	p := newInjector(rs, policy)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	// Should remain standard and no resource mutation
	if pod.Annotations[containerTypeAnnotation] != containerTypeStandard {
		t.Errorf("expected annotation unchanged, got %q", pod.Annotations[containerTypeAnnotation])
	}
}

func TestDefault_JavaPod_RecommendationAndAgentBothApplied(t *testing.T) {
	const deployName = "java-app"
	rs := replicaSet(deployName+"-rs", deployName)
	policy := javaRightsizePolicy(deployName)
	rec := rightsizeRecommendation(deployName)
	pod := javaPodOwnedByDeployment(deployName)
	p := newInjector(rs, policy, rec)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	// Resources from recommendation
	cpu := pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
	if cpu.MilliValue() != 200 {
		t.Errorf("expected CPU=200m from recommendation, got %s", cpu.String())
	}
	// Agent injected
	if pod.Annotations[containerTypeAnnotation] != containerTypeJava {
		t.Errorf("expected container-type=java, got %q", pod.Annotations[containerTypeAnnotation])
	}
	jto := envValue(pod.Spec.Containers[0].Env)
	if jto == "" {
		t.Error("expected JAVA_TOOL_OPTIONS to be set")
	}
}
