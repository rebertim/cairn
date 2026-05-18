/*
Copyright 2026 The Cairn Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package detector provides heuristics for identifying workload runtimes
// (e.g. Java/JVM) from container specs without requiring running pods.
package detector

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
	// Generic catch-all — covers custom images tagged with "-jdk" / "-jre" / "-java<version>"
	"-jdk",
	"-jre",
	"-java",
}

// javaEnvVars are environment variable names whose presence is a definitive
// or strong signal that the container runs on a JVM.
var javaEnvVars = []string{
	"JAVA_HOME",               // set by virtually every JDK/JRE image
	"JAVA_TOOL_OPTIONS",       // JVM reads this automatically
	"_JAVA_OPTIONS",           // older JVM fallback
	"JDK_JAVA_OPTIONS",        // Java 9+ official override
	"JAVA_OPTS",               // Spring Boot, Tomcat, WildFly convention
	"JVM_OPTS",                // alternative convention
	"CATALINA_OPTS",           // Tomcat-specific
	"CATALINA_HOME",           // Tomcat install dir — definitive Tomcat signal
	"CATALINA_BASE",           // Tomcat instance dir
	"GRADLE_OPTS",             // Gradle daemon JVM flags (dev images)
	"MAVEN_OPTS",              // Maven JVM flags (dev/CI images)
	"SPRING_PROFILES_ACTIVE",  // Spring Boot — extremely common in enterprise Java
	"SPRING_APPLICATION_NAME", // Spring Boot application identity
	"QUARKUS_PROFILE",         // Quarkus framework
	"MICRONAUT_ENVIRONMENTS",  // Micronaut framework
}

// IsJavaContainer returns true if any heuristic signals a JVM in this container.
// It inspects the image name, environment variables, and command/args — all
// available from the pod template spec without requiring running pods.
func IsJavaContainer(c corev1.Container) bool {
	return hasJavaImage(c.Image) || hasJavaEnv(c.Env) || hasJavaCommand(c.Command, c.Args)
}

// HasJavaContainers reports whether any container in the slice is a Java container.
func HasJavaContainers(containers []corev1.Container) bool {
	return slices.ContainsFunc(containers, IsJavaContainer)
}

// hasJavaImage matches the image name (without registry prefix or tag/digest)
// against javaImagePatterns.
func hasJavaImage(image string) bool {
	name := image
	if i := strings.LastIndex(image, "/"); i >= 0 {
		name = image[i+1:]
	}
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

// hasJavaCommand checks whether the container command or args invoke the java
// binary or reference a JAR file.
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
		if strings.HasPrefix(lower, "-xmx") || strings.HasPrefix(lower, "-xms") {
			return true
		}
	}
	return false
}
