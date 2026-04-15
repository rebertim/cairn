package webhook

import (
	"testing"

	v1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// --- updateJVMOpts ---

func TestUpdateJVMOpts_StripsOldStyleXmxXms_AppendsPercentage(t *testing.T) {
	flags := &v1alpha1.JVMFlags{MaxRAMPercentage: "73.14", InitialRAMPercentage: "73.14"}
	got := updateJVMOpts("-Xmx256m -Xms128m -verbose:gc", flags)
	want := "-verbose:gc -XX:MaxRAMPercentage=73.14 -XX:InitialRAMPercentage=73.14"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestUpdateJVMOpts_StripsExistingMaxRAMPercentage(t *testing.T) {
	flags := &v1alpha1.JVMFlags{MaxRAMPercentage: "75.00"}
	got := updateJVMOpts("-XX:MaxRAMPercentage=50.00 -XX:InitialRAMPercentage=50.00 -verbose:gc", flags)
	want := "-verbose:gc -XX:MaxRAMPercentage=75.00"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestUpdateJVMOpts_PreservesJavaAgent(t *testing.T) {
	flags := &v1alpha1.JVMFlags{MaxRAMPercentage: "75.00"}
	got := updateJVMOpts("-javaagent:/cairn/agent.jar -Xmx128m", flags)
	want := "-javaagent:/cairn/agent.jar -XX:MaxRAMPercentage=75.00"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestUpdateJVMOpts_EmptyExisting(t *testing.T) {
	flags := &v1alpha1.JVMFlags{MaxRAMPercentage: "75.00"}
	got := updateJVMOpts("", flags)
	if got != "-XX:MaxRAMPercentage=75.00" {
		t.Errorf("expected '-XX:MaxRAMPercentage=75.00', got %q", got)
	}
}

func TestUpdateJVMOpts_NoInitialWhenFlagEmpty(t *testing.T) {
	// InitialRAMPercentage is "" → stripped old, no new one appended
	flags := &v1alpha1.JVMFlags{MaxRAMPercentage: "75.00"}
	got := updateJVMOpts("-XX:InitialRAMPercentage=50.00", flags)
	if got != "-XX:MaxRAMPercentage=75.00" {
		t.Errorf("expected '-XX:MaxRAMPercentage=75.00', got %q", got)
	}
}

func TestUpdateJVMOpts_BothPercentages(t *testing.T) {
	flags := &v1alpha1.JVMFlags{MaxRAMPercentage: "75.00", InitialRAMPercentage: "75.00"}
	got := updateJVMOpts("", flags)
	want := "-XX:MaxRAMPercentage=75.00 -XX:InitialRAMPercentage=75.00"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestUpdateJVMOpts_MultipleExtraFlags_Preserved(t *testing.T) {
	flags := &v1alpha1.JVMFlags{MaxRAMPercentage: "75.00"}
	got := updateJVMOpts("-javaagent:/cairn/agent.jar -XX:+UseG1GC -Xmx128m -Xms64m", flags)
	want := "-javaagent:/cairn/agent.jar -XX:+UseG1GC -XX:MaxRAMPercentage=75.00"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

// --- applyRecommendedResources ---

func TestApplyRecommendedResources_SetsRequests(t *testing.T) {
	pod := podWith("app", "")
	cpuQ := resource.MustParse("200m")
	memQ := resource.MustParse("128Mi")
	rec := recForContainer("app", &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    cpuQ,
			corev1.ResourceMemory: memQ,
		},
	}, nil)

	applyRecommendedResources(pod, rec)

	gotCPU := pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
	if gotCPU.Cmp(cpuQ) != 0 {
		t.Errorf("CPU: want %s, got %s", cpuQ.String(), gotCPU.String())
	}
	gotMem := pod.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory]
	if gotMem.Cmp(memQ) != 0 {
		t.Errorf("Memory: want %s, got %s", memQ.String(), gotMem.String())
	}
}

func TestApplyRecommendedResources_SkipsUnknownContainer(t *testing.T) {
	pod := podWith("app", "")
	rec := recForContainer("sidecar", &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")},
	}, nil)

	applyRecommendedResources(pod, rec)

	if pod.Spec.Containers[0].Resources.Requests != nil {
		t.Error("container with different name should not be touched")
	}
}

func TestApplyRecommendedResources_NilRecommended_NoCrash(t *testing.T) {
	pod := podWith("app", "")
	rec := &v1alpha1.RightsizeRecommendation{
		Status: v1alpha1.RightsizeRecommendationStatus{
			Containers: []v1alpha1.ContainerRecommendation{
				{ContainerName: "app", Recommended: nil},
			},
		},
	}
	// should not panic
	applyRecommendedResources(pod, rec)
}

func TestApplyRecommendedResources_SetsJVMFlags(t *testing.T) {
	pod := podWith("java", "")
	rec := recForContainer("java", nil, &v1alpha1.JVMRecommendation{
		RecommendedFlags: &v1alpha1.JVMFlags{MaxRAMPercentage: "75.00", InitialRAMPercentage: "75.00"},
	})

	applyRecommendedResources(pod, rec)

	val := envValue(pod.Spec.Containers[0].Env)
	if val != "-XX:MaxRAMPercentage=75.00 -XX:InitialRAMPercentage=75.00" {
		t.Errorf("expected percentage flags in JAVA_TOOL_OPTIONS, got %q", val)
	}
}

func TestApplyRecommendedResources_JVMFlagsPreservesJavaAgent(t *testing.T) {
	pod := podWith("java", "-javaagent:/cairn/agent.jar")
	rec := recForContainer("java", nil, &v1alpha1.JVMRecommendation{
		RecommendedFlags: &v1alpha1.JVMFlags{MaxRAMPercentage: "75.00"},
	})

	applyRecommendedResources(pod, rec)

	val := envValue(pod.Spec.Containers[0].Env)
	if val != "-javaagent:/cairn/agent.jar -XX:MaxRAMPercentage=75.00" {
		t.Errorf("expected javaagent preserved, got %q", val)
	}
}

func TestApplyRecommendedResources_NilJVM_NoJavaToolOptions(t *testing.T) {
	pod := podWith("app", "")
	rec := recForContainer("app", &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")},
	}, nil) // nil JVM

	applyRecommendedResources(pod, rec)

	val := envValue(pod.Spec.Containers[0].Env)
	if val != "" {
		t.Errorf("JAVA_TOOL_OPTIONS should not be set when JVM is nil, got %q", val)
	}
}

// --- helpers ---

func podWith(containerName, javaToolOptions string) *corev1.Pod {
	c := corev1.Container{Name: containerName}
	if javaToolOptions != "" {
		c.Env = []corev1.EnvVar{{Name: "JAVA_TOOL_OPTIONS", Value: javaToolOptions}}
	}
	return &corev1.Pod{
		Spec: corev1.PodSpec{Containers: []corev1.Container{c}},
	}
}

func recForContainer(name string, res *corev1.ResourceRequirements, jvm *v1alpha1.JVMRecommendation) *v1alpha1.RightsizeRecommendation {
	return &v1alpha1.RightsizeRecommendation{
		Status: v1alpha1.RightsizeRecommendationStatus{
			Containers: []v1alpha1.ContainerRecommendation{
				{
					ContainerName: name,
					Recommended:   res,
					JVM:           jvm,
				},
			},
		},
	}
}
