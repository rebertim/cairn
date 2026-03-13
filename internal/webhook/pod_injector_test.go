package webhook

import (
	"testing"

	v1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// --- updateJVMOpts ---

func TestUpdateJVMOpts_StripsExistingXmxXms_AppendsNew(t *testing.T) {
	flags := &v1alpha1.JVMFlags{Xmx: "512m", Xms: "256m"}
	got := updateJVMOpts("-Xmx256m -Xms128m -verbose:gc", flags)
	want := "-verbose:gc -Xmx512m -Xms256m"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestUpdateJVMOpts_PreservesJavaAgent(t *testing.T) {
	flags := &v1alpha1.JVMFlags{Xmx: "256m"}
	got := updateJVMOpts("-javaagent:/cairn/agent.jar -Xmx128m", flags)
	want := "-javaagent:/cairn/agent.jar -Xmx256m"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestUpdateJVMOpts_EmptyExisting(t *testing.T) {
	flags := &v1alpha1.JVMFlags{Xmx: "512m"}
	got := updateJVMOpts("", flags)
	if got != "-Xmx512m" {
		t.Errorf("expected '-Xmx512m', got %q", got)
	}
}

func TestUpdateJVMOpts_NoXmsWhenFlagEmpty(t *testing.T) {
	// Flags.Xms is "" → stripped old -Xms, no new one appended
	flags := &v1alpha1.JVMFlags{Xmx: "512m"}
	got := updateJVMOpts("-Xms128m", flags)
	if got != "-Xmx512m" {
		t.Errorf("expected '-Xmx512m', got %q", got)
	}
}

func TestUpdateJVMOpts_BothXmxAndXms(t *testing.T) {
	flags := &v1alpha1.JVMFlags{Xmx: "512m", Xms: "512m"}
	got := updateJVMOpts("", flags)
	want := "-Xmx512m -Xms512m"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestUpdateJVMOpts_MultipleExtraFlags_Preserved(t *testing.T) {
	flags := &v1alpha1.JVMFlags{Xmx: "256m"}
	got := updateJVMOpts("-javaagent:/cairn/agent.jar -XX:+UseG1GC -Xmx128m -Xms64m", flags)
	want := "-javaagent:/cairn/agent.jar -XX:+UseG1GC -Xmx256m"
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
		RecommendedFlags: &v1alpha1.JVMFlags{Xmx: "256m", Xms: "256m"},
	})

	applyRecommendedResources(pod, rec)

	val := envValue(pod.Spec.Containers[0].Env, "JAVA_TOOL_OPTIONS")
	if val != "-Xmx256m -Xms256m" {
		t.Errorf("expected JAVA_TOOL_OPTIONS='-Xmx256m -Xms256m', got %q", val)
	}
}

func TestApplyRecommendedResources_JVMFlagsPreservesJavaAgent(t *testing.T) {
	// -javaagent should already be in JAVA_TOOL_OPTIONS when applyRecommendedResources runs
	pod := podWith("java", "-javaagent:/cairn/agent.jar")
	rec := recForContainer("java", nil, &v1alpha1.JVMRecommendation{
		RecommendedFlags: &v1alpha1.JVMFlags{Xmx: "256m"},
	})

	applyRecommendedResources(pod, rec)

	val := envValue(pod.Spec.Containers[0].Env, "JAVA_TOOL_OPTIONS")
	if val != "-javaagent:/cairn/agent.jar -Xmx256m" {
		t.Errorf("expected javaagent preserved, got %q", val)
	}
}

func TestApplyRecommendedResources_NilJVM_NoJavaToolOptions(t *testing.T) {
	pod := podWith("app", "")
	rec := recForContainer("app", &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")},
	}, nil) // nil JVM

	applyRecommendedResources(pod, rec)

	val := envValue(pod.Spec.Containers[0].Env, "JAVA_TOOL_OPTIONS")
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
