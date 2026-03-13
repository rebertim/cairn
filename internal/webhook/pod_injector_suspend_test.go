package webhook

// Suspend is a hard kill-switch: when policy.Spec.Suspended=true the webhook
// must return immediately without any mutation — no annotations, no labels,
// no volumes, no port additions, no JAVA_TOOL_OPTIONS changes, and no
// resource patches.  Every test here verifies one facet of that guarantee.

import (
	"context"
	"testing"

	v1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// suspendedInjector wires up a PodInjector whose policy always has
// Suspended=true.  pass recommendation=true to also create a matching rec.
func suspendedSetup(t *testing.T, isJava, withRec bool) (*PodInjector, *corev1.Pod) {
	t.Helper()
	const ns, deployName = "default", "my-app"
	rs := replicaSet(ns, deployName+"-rs", deployName)

	var pol *v1alpha1.RightsizePolicy
	if isJava {
		pol = javaRightsizePolicy(ns, "policy", deployName)
	} else {
		pol = rightsizePolicy(ns, "policy", deployName)
	}
	pol.Spec.Suspended = true

	var pod *corev1.Pod
	if isJava {
		pod = javaPodOwnedByDeployment(ns, deployName)
	} else {
		pod = deploymentOwnedPod(ns, deployName)
	}

	objs := []runtime.Object{rs, pol}
	if withRec {
		objs = append(objs, rightsizeRecommendation(ns, "Deployment", deployName, "app"))
	}
	return newInjector(objs...), pod
}

// TestSuspended_NoContainerTypeAnnotation verifies that a suspended policy
// leaves the containerTypeAnnotation completely unset — the pod is not
// labelled as "standard" or "java".
func TestSuspended_NoContainerTypeAnnotation(t *testing.T) {
	p, pod := suspendedSetup(t, false, false)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if v := pod.Annotations[containerTypeAnnotation]; v != "" {
		t.Errorf("containerTypeAnnotation must be empty when suspended, got %q", v)
	}
}

// TestSuspended_JavaPod_NoContainerTypeAnnotation checks the same for a Java
// pod: even though the heuristics would detect Java, the annotation is not set.
func TestSuspended_JavaPod_NoContainerTypeAnnotation(t *testing.T) {
	p, pod := suspendedSetup(t, true, false)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if v := pod.Annotations[containerTypeAnnotation]; v != "" {
		t.Errorf("containerTypeAnnotation must be empty for suspended Java pod, got %q", v)
	}
}

// TestSuspended_JavaPod_NoInjectedAnnotation confirms that the
// cairn.io/agent-injected annotation is not written on suspended pods.
func TestSuspended_JavaPod_NoInjectedAnnotation(t *testing.T) {
	p, pod := suspendedSetup(t, true, false)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if v := pod.Annotations[injectedAnnotation]; v != "" {
		t.Errorf("injectedAnnotation must be absent when suspended, got %q", v)
	}
}

// TestSuspended_JavaPod_NoInjectedLabel confirms that the
// cairn.io/agent-injected label is not written on suspended pods.
func TestSuspended_JavaPod_NoInjectedLabel(t *testing.T) {
	p, pod := suspendedSetup(t, true, false)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if v := pod.Labels[injectedAnnotation]; v != "" {
		t.Errorf("injected label must be absent when suspended, got %q", v)
	}
}

// TestSuspended_JavaPod_NoAgentVolume ensures no cairn-agent ImageVolume is
// injected when the policy is suspended.
func TestSuspended_JavaPod_NoAgentVolume(t *testing.T) {
	p, pod := suspendedSetup(t, true, false)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	for _, v := range pod.Spec.Volumes {
		if v.Name == agentVolumeName {
			t.Errorf("cairn-agent volume must not be added when suspended")
		}
	}
}

// TestSuspended_JavaPod_NoVolumeMount ensures no container gets a
// /cairn volume mount when the policy is suspended.
func TestSuspended_JavaPod_NoVolumeMount(t *testing.T) {
	p, pod := suspendedSetup(t, true, false)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	for _, c := range pod.Spec.Containers {
		for _, vm := range c.VolumeMounts {
			if vm.Name == agentVolumeName {
				t.Errorf("container %q must not have a cairn volume mount when suspended", c.Name)
			}
		}
	}
}

// TestSuspended_JavaPod_NoCairnMetricsPort verifies that the 9404 cairn-metrics
// port is not appended to any container when suspended.
func TestSuspended_JavaPod_NoCairnMetricsPort(t *testing.T) {
	p, pod := suspendedSetup(t, true, false)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	for _, c := range pod.Spec.Containers {
		for _, port := range c.Ports {
			if port.ContainerPort == agentMetricsPort {
				t.Errorf("container %q must not have cairn-metrics port when suspended", c.Name)
			}
		}
	}
}

// TestSuspended_JavaPod_JavaToolOptionsNotSet verifies that JAVA_TOOL_OPTIONS
// is not added when the policy is suspended and no existing value is present.
func TestSuspended_JavaPod_JavaToolOptionsNotSet(t *testing.T) {
	p, pod := suspendedSetup(t, true, false)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if v := envValue(pod.Spec.Containers[0].Env, "JAVA_TOOL_OPTIONS"); v != "" {
		t.Errorf("JAVA_TOOL_OPTIONS must not be set when suspended, got %q", v)
	}
}

// TestSuspended_JavaPod_ExistingJavaToolOptions_Unchanged ensures that if the
// pod already carries a JAVA_TOOL_OPTIONS value (set by the user in the
// Deployment spec), that value is left untouched when suspended.
func TestSuspended_JavaPod_ExistingJavaToolOptions_Unchanged(t *testing.T) {
	const ns, deployName = "default", "my-app"
	rs := replicaSet(ns, deployName+"-rs", deployName)
	pol := javaRightsizePolicy(ns, "policy", deployName)
	pol.Spec.Suspended = true
	rec := rightsizeRecommendation(ns, "Deployment", deployName, "app")

	pod := javaPodOwnedByDeployment(ns, deployName)
	// Simulate user-defined JAVA_TOOL_OPTIONS in the Deployment spec.
	pod.Spec.Containers[0].Env = []corev1.EnvVar{
		{Name: "JAVA_TOOL_OPTIONS", Value: "-XX:+UseG1GC -Xmx512m"},
	}

	p := newInjector(rs, pol, rec)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	got := envValue(pod.Spec.Containers[0].Env, "JAVA_TOOL_OPTIONS")
	if got != "-XX:+UseG1GC -Xmx512m" {
		t.Errorf("existing JAVA_TOOL_OPTIONS must be preserved unchanged, got %q", got)
	}
}

// TestSuspended_ResourcesNotApplied verifies that even when a
// RightsizeRecommendation exists for the workload, resources are NOT applied
// to the pod when the policy is suspended.
func TestSuspended_ResourcesNotApplied(t *testing.T) {
	p, pod := suspendedSetup(t, false, true) // withRec=true
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if pod.Spec.Containers[0].Resources.Requests != nil {
		t.Error("resources must not be applied when policy is suspended")
	}
}

// TestSuspended_ExistingResources_NotOverwritten is the key safety test:
// a pod that already has resources set by its Deployment spec must NOT have
// those resources replaced when the policy is suspended.
func TestSuspended_ExistingResources_NotOverwritten(t *testing.T) {
	const ns, deployName = "default", "my-app"
	rs := replicaSet(ns, deployName+"-rs", deployName)
	pol := rightsizePolicy(ns, "policy", deployName)
	pol.Spec.Suspended = true
	rec := rightsizeRecommendation(ns, "Deployment", deployName, "app") // 200m CPU

	pod := deploymentOwnedPod(ns, deployName)
	// The Deployment spec already sets resources — suspension must preserve them.
	existingCPU := resource.MustParse("100m")
	pod.Spec.Containers[0].Resources.Requests = corev1.ResourceList{
		corev1.ResourceCPU: existingCPU,
	}

	p := newInjector(rs, pol, rec)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	gotCPU := pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
	if gotCPU.Cmp(existingCPU) != 0 {
		t.Errorf("existing CPU request must be preserved: want %s, got %s",
			existingCPU.String(), gotCPU.String())
	}
}

// TestSuspended_JVMFlagsNotApplied ensures that JVM flags from a
// recommendation are not written into JAVA_TOOL_OPTIONS when suspended.
func TestSuspended_JVMFlagsNotApplied(t *testing.T) {
	const ns, deployName = "default", "my-app"
	rs := replicaSet(ns, deployName+"-rs", deployName)
	pol := javaRightsizePolicy(ns, "policy", deployName)
	pol.Spec.Suspended = true

	// Recommendation that carries JVM flags.
	recName := "deployment-" + deployName
	cpuQ := resource.MustParse("200m")
	memQ := resource.MustParse("128Mi")
	rec := &v1alpha1.RightsizeRecommendation{
		ObjectMeta: metav1.ObjectMeta{Name: recName, Namespace: ns},
		Spec: v1alpha1.RightsizeRecommendationSpec{
			TargetRef: v1alpha1.TargetRef{Kind: "Deployment", Name: deployName},
		},
		Status: v1alpha1.RightsizeRecommendationStatus{
			Containers: []v1alpha1.ContainerRecommendation{{
				ContainerName: "app",
				Recommended: &corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    cpuQ,
						corev1.ResourceMemory: memQ,
					},
				},
				JVM: &v1alpha1.JVMRecommendation{
					RecommendedFlags: &v1alpha1.JVMFlags{Xmx: "256m", Xms: "256m"},
				},
			}},
		},
	}

	pod := javaPodOwnedByDeployment(ns, deployName)
	p := newInjector(rs, pol, rec)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if v := envValue(pod.Spec.Containers[0].Env, "JAVA_TOOL_OPTIONS"); v != "" {
		t.Errorf("JAVA_TOOL_OPTIONS must remain empty when suspended, got %q", v)
	}
}

// TestSuspended_MultiContainer_NeitherTouched checks a pod with two containers
// where both would normally be affected. With the policy suspended, neither
// should receive any annotation, resource patch, or volume mount.
func TestSuspended_MultiContainer_NeitherTouched(t *testing.T) {
	const ns, deployName = "default", "my-app"
	rs := replicaSet(ns, deployName+"-rs", deployName)
	pol := javaRightsizePolicy(ns, "policy", deployName)
	pol.Spec.Suspended = true

	trueVal := true
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployName + "-pod",
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{{
				Kind:       "ReplicaSet",
				Name:       deployName + "-rs",
				Controller: &trueVal,
			}},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "app", Image: "eclipse-temurin:21-jdk-alpine"},
				{Name: "sidecar", Image: "nginx:alpine"},
			},
		},
	}

	p := newInjector(rs, pol)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if pod.Annotations[containerTypeAnnotation] != "" {
		t.Errorf("no annotation must be set when suspended, got %q", pod.Annotations[containerTypeAnnotation])
	}
	for _, c := range pod.Spec.Containers {
		if c.Resources.Requests != nil {
			t.Errorf("container %q must have no resources when suspended", c.Name)
		}
		for _, vm := range c.VolumeMounts {
			if vm.Name == agentVolumeName {
				t.Errorf("container %q must not have cairn volume mount when suspended", c.Name)
			}
		}
	}
	if len(pod.Spec.Volumes) != 0 {
		t.Errorf("no volumes must be added when suspended, got %d", len(pod.Spec.Volumes))
	}
}

// TestSuspended_ReturnsNilError ensures that a suspended policy is a clean
// no-op and never causes an error to propagate to the API server.
func TestSuspended_ReturnsNilError(t *testing.T) {
	p, pod := suspendedSetup(t, true, true)
	err := p.Default(context.Background(), pod)
	if err != nil {
		t.Errorf("suspended policy must return nil error, got: %v", err)
	}
}

// TestSuspended_ForcedInjectAnnotation checks that even when a pod carries the
// explicit opt-in annotation cairn.io/inject-agent=true, a suspended policy
// still prevents injection.
func TestSuspended_ForcedInjectAnnotation_StillPreventsInjection(t *testing.T) {
	const ns, deployName = "default", "my-app"
	rs := replicaSet(ns, deployName+"-rs", deployName)
	pol := javaRightsizePolicy(ns, "policy", deployName)
	pol.Spec.Suspended = true

	pod := deploymentOwnedPod(ns, deployName) // nginx — not Java by image
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations["cairn.io/inject-agent"] = "true" // explicit opt-in

	p := newInjector(rs, pol)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if pod.Annotations[containerTypeAnnotation] != "" {
		t.Errorf("forced inject-agent annotation must be ignored when suspended, got %q",
			pod.Annotations[containerTypeAnnotation])
	}
	if len(pod.Spec.Volumes) != 0 {
		t.Error("no volumes should be added when suspended even with inject-agent=true")
	}
}

// TestSuspended_AnnotationsMapNotCreated verifies that if the pod starts with
// nil annotations and nil labels, the webhook does not create those maps when
// suspended (the pod object is fully unchanged).
func TestSuspended_NilAnnotationsAndLabels_NothingCreated(t *testing.T) {
	const ns, deployName = "default", "my-app"
	rs := replicaSet(ns, deployName+"-rs", deployName)
	pol := rightsizePolicy(ns, "policy", deployName)
	pol.Spec.Suspended = true

	pod := deploymentOwnedPod(ns, deployName)
	pod.Annotations = nil
	pod.Labels = nil

	p := newInjector(rs, pol)
	if err := p.Default(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if pod.Annotations != nil {
		t.Errorf("annotations map must not be created when suspended, got %v", pod.Annotations)
	}
	if pod.Labels != nil {
		t.Errorf("labels map must not be created when suspended, got %v", pod.Labels)
	}
}
