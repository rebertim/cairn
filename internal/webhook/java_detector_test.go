/*
Copyright 2026 The Cairn Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package webhook

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
