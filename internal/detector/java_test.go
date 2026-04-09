/*
Copyright 2026 The Cairn Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package detector

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
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
	if !hasJavaImage("graalvm:21") {
		t.Error("graalvm image should be detected")
	}
}

func TestHasJavaImage_GraalVMBehindRegistry(t *testing.T) {
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

func TestHasJavaEnv_SpringProfilesActive(t *testing.T) {
	env := []corev1.EnvVar{{Name: "SPRING_PROFILES_ACTIVE", Value: "prod"}}
	if !hasJavaEnv(env) {
		t.Error("SPRING_PROFILES_ACTIVE should be detected as Java (Spring Boot)")
	}
}

func TestHasJavaEnv_SpringApplicationName(t *testing.T) {
	env := []corev1.EnvVar{{Name: "SPRING_APPLICATION_NAME", Value: "my-service"}}
	if !hasJavaEnv(env) {
		t.Error("SPRING_APPLICATION_NAME should be detected as Java (Spring Boot)")
	}
}

func TestHasJavaEnv_QuarkusProfile(t *testing.T) {
	env := []corev1.EnvVar{{Name: "QUARKUS_PROFILE", Value: "prod"}}
	if !hasJavaEnv(env) {
		t.Error("QUARKUS_PROFILE should be detected as Java (Quarkus)")
	}
}

func TestHasJavaEnv_MicronautEnvironments(t *testing.T) {
	env := []corev1.EnvVar{{Name: "MICRONAUT_ENVIRONMENTS", Value: "prod"}}
	if !hasJavaEnv(env) {
		t.Error("MICRONAUT_ENVIRONMENTS should be detected as Java (Micronaut)")
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

// --- IsJavaContainer ---

func TestIsJavaContainer_ByImage(t *testing.T) {
	c := corev1.Container{Name: "app", Image: "eclipse-temurin:21"}
	if !IsJavaContainer(c) {
		t.Error("container with Java image should be Java")
	}
}

func TestIsJavaContainer_ByEnv(t *testing.T) {
	c := corev1.Container{
		Name:  "app",
		Image: "scratch",
		Env:   []corev1.EnvVar{{Name: "JAVA_HOME", Value: "/jdk"}},
	}
	if !IsJavaContainer(c) {
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
	if !IsJavaContainer(c) {
		t.Error("container running java should be Java")
	}
}

func TestIsJavaContainer_NonJava(t *testing.T) {
	c := corev1.Container{Name: "nginx", Image: "nginx:alpine"}
	if IsJavaContainer(c) {
		t.Error("nginx container should not be Java")
	}
}
