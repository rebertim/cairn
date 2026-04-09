/*
Copyright 2026 The Cairn Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package webhook

import (
	"github.com/sempex/cairn/internal/detector"
	corev1 "k8s.io/api/core/v1"
)

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

	return detector.HasJavaContainers(pod.Spec.Containers)
}
