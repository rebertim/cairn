/*
Copyright 2026 The Cairn Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package webhook

import (
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// javaImagePatterns are substrings matched (case-insensitively) against the
// image name portion (everything before the colon/digest). Any match means
// the container is almost certainly running a JVM.
var javaImagePatterns = []string{
	// Official / community OpenJDK distributions
	"openjdk",
	"eclipse-temurin",
	"temurin",
	"amazoncorretto",
	"corretto",
	"azul/zulu",
	"zulu",
	"bellsoft/liberica",
	"liberica",
	"ibm-semeru",
	"semeru",
	"sapmachine",
	"graalvm",
	"mandrel",
	// Servlet containers / app servers
	"tomcat",
	"jetty",
	"wildfly",
	"jboss",
	"payara",
	"glassfish",
	"websphere",
	"weblogic",
	// Generic catch-all — covers custom images tagged with "-jdk" / "-jre"
	"-jdk",
	"-jre",
}

// javaEnvVars are environment variable names whose presence is a definitive
// or strong signal that the container runs on a JVM.
var javaEnvVars = []string{
	"JAVA_HOME",         // set by virtually every JDK/JRE image
	"JAVA_TOOL_OPTIONS", // JVM reads this automatically
	"_JAVA_OPTIONS",     // older JVM fallback
	"JDK_JAVA_OPTIONS",  // Java 9+ official override
	"JAVA_OPTS",         // Spring Boot, Tomcat, WildFly convention
	"JVM_OPTS",          // alternative convention
	"CATALINA_OPTS",     // Tomcat-specific
	"GRADLE_OPTS",       // Gradle daemon JVM flags (dev images)
	"MAVEN_OPTS",        // Maven JVM flags (dev/CI images)
}

// isJavaPod reports whether a pod appears to be running a Java workload.
// It honours explicit opt-in/opt-out annotations first, then falls back to
// heuristic inspection of each container.
//
// Annotation semantics:
//
//	cairn.io/inject-agent: "true"  → always inject (even if detection misses)
//	cairn.io/inject-agent: "false" → never inject (explicit opt-out)
func isJavaPod(pod *corev1.Pod) bool {
	switch pod.Annotations["cairn.io/inject-agent"] {
	case "false":
		return false
	case "true":
		return true
	}

	return slices.ContainsFunc(pod.Spec.Containers, isJavaContainer)
}

// isJavaContainer returns true if any heuristic signals a JVM in this container.
func isJavaContainer(c corev1.Container) bool {
	return hasJavaImage(c.Image) || hasJavaEnv(c.Env) || hasJavaCommand(c.Command, c.Args)
}

// hasJavaImage matches the image name (without registry prefix or tag/digest)
// against javaImagePatterns.
func hasJavaImage(image string) bool {
	// Strip registry prefix: everything up to the last '/' before the name.
	name := image
	if i := strings.LastIndex(image, "/"); i >= 0 {
		name = image[i+1:]
	}
	// Strip tag or digest so patterns like "-jdk" match "eclipse-temurin:17-jdk-alpine".
	lower := strings.ToLower(name)
	for _, p := range javaImagePatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// hasJavaEnv checks whether any of the container's env vars is a known JVM signal.
func hasJavaEnv(env []corev1.EnvVar) bool {
	return slices.ContainsFunc(env, func(e corev1.EnvVar) bool {
		return slices.Contains(javaEnvVars, e.Name)
	})
}

// hasJavaCommand checks whether the container command or args invoke the java binary
// or reference a JAR file — a reliable signal for containers with custom entrypoints.
func hasJavaCommand(command, args []string) bool {
	tokens := append(command, args...)
	for _, tok := range tokens {
		lower := strings.ToLower(tok)
		if lower == "java" || strings.HasSuffix(lower, "/java") {
			return true
		}
		if strings.HasSuffix(lower, ".jar") {
			return true
		}
		// JVM flags passed directly as the command (rare but seen in distroless images)
		if strings.HasPrefix(lower, "-xmx") || strings.HasPrefix(lower, "-xms") {
			return true
		}
	}
	return false
}
