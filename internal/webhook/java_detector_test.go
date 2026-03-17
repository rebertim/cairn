package webhook

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// --- hasJavaImage ---

func TestHasJavaImage_OpenJDK(t *testing.T) {
	if !hasJavaImage("openjdk:17-alpine") {
		t.Error("openjdk should be detected as Java")
	}
}

func TestHasJavaImage_EclipseTemurin(t *testing.T) {
	if !hasJavaImage("eclipse-temurin:21-jdk-alpine") {
		t.Error("eclipse-temurin should be detected as Java")
	}
}

func TestHasJavaImage_FullRegistry_StillDetected(t *testing.T) {
	// Registry prefix must be stripped before matching
	if !hasJavaImage("my-registry.example.com/team/eclipse-temurin:17") {
		t.Error("temurin behind a private registry should be detected")
	}
}

func TestHasJavaImage_JDKSuffix(t *testing.T) {
	if !hasJavaImage("my-company/app-jdk:latest") {
		t.Error("image with -jdk suffix should be detected")
	}
}

func TestHasJavaImage_JRESuffix(t *testing.T) {
	if !hasJavaImage("myapp-jre:1.0") {
		t.Error("image with -jre suffix should be detected")
	}
}

func TestHasJavaImage_GraalVM(t *testing.T) {
	// hasJavaImage matches the last path component; "graalvm:21" has "graalvm" in its name
	if !hasJavaImage("graalvm:21") {
		t.Error("graalvm image should be detected")
	}
}

func TestHasJavaImage_GraalVMBehindRegistry(t *testing.T) {
	// Registry + org prefix: last component is "graalvmce" — still contains "graalvm"
	if !hasJavaImage("ghcr.io/org/graalvmce:21") {
		t.Error("graalvmce image behind registry should be detected")
	}
}

func TestHasJavaImage_Tomcat(t *testing.T) {
	if !hasJavaImage("tomcat:10-jdk17") {
		t.Error("tomcat image should be detected")
	}
}

func TestHasJavaImage_CaseInsensitive(t *testing.T) {
	if !hasJavaImage("OpenJDK:17") {
		t.Error("detection should be case-insensitive")
	}
}

func TestHasJavaImage_Nginx_NotJava(t *testing.T) {
	if hasJavaImage("nginx:alpine") {
		t.Error("nginx should not be detected as Java")
	}
}

func TestHasJavaImage_Python_NotJava(t *testing.T) {
	if hasJavaImage("python:3.11-slim") {
		t.Error("python should not be detected as Java")
	}
}

func TestHasJavaImage_Alpine_NotJava(t *testing.T) {
	if hasJavaImage("alpine:3.18") {
		t.Error("plain alpine should not be detected as Java")
	}
}

// --- hasJavaEnv ---

func TestHasJavaEnv_JavaHome(t *testing.T) {
	env := []corev1.EnvVar{{Name: "JAVA_HOME", Value: "/usr/lib/jvm/java-17"}}
	if !hasJavaEnv(env) {
		t.Error("JAVA_HOME should be detected")
	}
}

func TestHasJavaEnv_JavaToolOptions(t *testing.T) {
	env := []corev1.EnvVar{{Name: "JAVA_TOOL_OPTIONS", Value: "-Xmx512m"}}
	if !hasJavaEnv(env) {
		t.Error("JAVA_TOOL_OPTIONS should be detected")
	}
}

func TestHasJavaEnv_JavaOpts(t *testing.T) {
	env := []corev1.EnvVar{{Name: "JAVA_OPTS", Value: "-server"}}
	if !hasJavaEnv(env) {
		t.Error("JAVA_OPTS should be detected")
	}
}

func TestHasJavaEnv_CatalinaOpts(t *testing.T) {
	env := []corev1.EnvVar{{Name: "CATALINA_OPTS", Value: "-Xms256m"}}
	if !hasJavaEnv(env) {
		t.Error("CATALINA_OPTS should be detected")
	}
}

func TestHasJavaEnv_UnrelatedEnvVars_NotDetected(t *testing.T) {
	env := []corev1.EnvVar{
		{Name: "PATH", Value: "/usr/bin"},
		{Name: "HOME", Value: "/root"},
	}
	if hasJavaEnv(env) {
		t.Error("unrelated env vars should not trigger Java detection")
	}
}

func TestHasJavaEnv_Empty_NotDetected(t *testing.T) {
	if hasJavaEnv(nil) {
		t.Error("nil env should not be detected as Java")
	}
}

// --- hasJavaCommand ---

func TestHasJavaCommand_JavaBinary(t *testing.T) {
	if !hasJavaCommand([]string{"java"}, []string{"-jar", "app.jar"}) {
		t.Error("'java' command should be detected")
	}
}

func TestHasJavaCommand_JavaBinaryFullPath(t *testing.T) {
	if !hasJavaCommand([]string{"/usr/bin/java"}, nil) {
		t.Error("full path to java binary should be detected")
	}
}

func TestHasJavaCommand_JarFile(t *testing.T) {
	if !hasJavaCommand(nil, []string{"-cp", "lib.jar", "app.jar"}) {
		t.Error(".jar argument should be detected")
	}
}

func TestHasJavaCommand_XmxFlag(t *testing.T) {
	// Distroless images sometimes start with JVM flags directly
	if !hasJavaCommand([]string{"-Xmx512m"}, nil) {
		t.Error("-Xmx flag in command should be detected")
	}
}

func TestHasJavaCommand_XmsFlag(t *testing.T) {
	if !hasJavaCommand([]string{"-Xms256m"}, nil) {
		t.Error("-Xms flag in command should be detected")
	}
}

func TestHasJavaCommand_NonJavaCommand(t *testing.T) {
	if hasJavaCommand([]string{"nginx"}, []string{"-g", "daemon off;"}) {
		t.Error("nginx command should not be detected as Java")
	}
}

func TestHasJavaCommand_Empty_NotDetected(t *testing.T) {
	if hasJavaCommand(nil, nil) {
		t.Error("empty command/args should not be detected")
	}
}

func TestHasJavaCommand_CaseInsensitive_Jar(t *testing.T) {
	if !hasJavaCommand(nil, []string{"APP.JAR"}) {
		t.Error(".JAR (uppercase) should be detected")
	}
}

// --- isJavaContainer ---

func TestIsJavaContainer_ByImage(t *testing.T) {
	c := corev1.Container{Name: "app", Image: "eclipse-temurin:21"}
	if !isJavaContainer(c) {
		t.Error("container with Java image should be Java")
	}
}

func TestIsJavaContainer_ByEnv(t *testing.T) {
	c := corev1.Container{
		Name:  "app",
		Image: "scratch",
		Env:   []corev1.EnvVar{{Name: "JAVA_HOME", Value: "/jdk"}},
	}
	if !isJavaContainer(c) {
		t.Error("container with JAVA_HOME should be Java")
	}
}

func TestIsJavaContainer_ByCommand(t *testing.T) {
	c := corev1.Container{
		Name:    "app",
		Image:   "busybox",
		Command: []string{"java"},
		Args:    []string{"-jar", "service.jar"},
	}
	if !isJavaContainer(c) {
		t.Error("container running java should be Java")
	}
}

func TestIsJavaContainer_NonJava(t *testing.T) {
	c := corev1.Container{Name: "nginx", Image: "nginx:alpine"}
	if isJavaContainer(c) {
		t.Error("nginx container should not be Java")
	}
}

// --- isJavaPod ---

func TestIsJavaPod_OptInAnnotation_AlwaysTrue(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{"cairn.io/inject-agent": "true"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "nginx:alpine"}},
		},
	}
	if !isJavaPod(pod) {
		t.Error("explicit opt-in annotation should always return true")
	}
}

func TestIsJavaPod_OptOutAnnotation_AlwaysFalse(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{"cairn.io/inject-agent": "false"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "eclipse-temurin:21"}},
		},
	}
	if isJavaPod(pod) {
		t.Error("explicit opt-out annotation should always return false even with Java image")
	}
}

func TestIsJavaPod_HeuristicDetection_JavaImage(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "openjdk:17"}},
		},
	}
	if !isJavaPod(pod) {
		t.Error("pod with Java container image should be detected as Java")
	}
}

func TestIsJavaPod_HeuristicDetection_NonJava(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "web", Image: "nginx:latest"}},
		},
	}
	if isJavaPod(pod) {
		t.Error("pod with non-Java image should not be detected as Java")
	}
}

func TestIsJavaPod_MultiContainer_OneJava(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "sidecar", Image: "envoy:latest"},
				{Name: "app", Image: "eclipse-temurin:21"},
			},
		},
	}
	if !isJavaPod(pod) {
		t.Error("pod with at least one Java container should be detected as Java")
	}
}
