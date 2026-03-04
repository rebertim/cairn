/*
Copyright 2026 The Cairn Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package webhook

import (
	"context"

	rightsizingv1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var podInjectorLog = logf.Log.WithName("pod-injector")

const (
	injectedAnnotation = "cairn.io/agent-injected"
	agentVolumeName    = "cairn-agent"
	agentMountPath     = "/cairn"
)

// +kubebuilder:webhook:path=/mutate-v1-pod,mutating=true,failurePolicy=ignore,sideEffects=None,groups="",resources=pods,verbs=create,versions=v1,name=mpod.cairn.io,admissionReviewVersions=v1
// +kubebuilder:rbac:groups=apps,resources=replicasets,verbs=get
// +kubebuilder:rbac:groups=rightsizing.cairn.io,resources=rightsizepolicies,verbs=list

// PodInjector injects the Cairn JVM agent into Java pods via a mutating webhook.
type PodInjector struct {
	Client     client.Client
	AgentImage string
}

// SetupPodInjectorWebhookWithManager registers the Pod mutating webhook with the manager.
func SetupPodInjectorWebhookWithManager(mgr ctrl.Manager, agentImage string) error {
	return ctrl.NewWebhookManagedBy(mgr, &corev1.Pod{}).
		WithDefaulter(&PodInjector{
			Client:     mgr.GetClient(),
			AgentImage: agentImage,
		}).
		Complete()
}

// Default implements webhook.CustomDefaulter.
func (p *PodInjector) Default(ctx context.Context, pod *corev1.Pod) error {
	log := podInjectorLog.WithValues("pod", pod.Name, "namespace", pod.Namespace)

	if !isJavaPod(pod) {
		return nil
	}
	if pod.Annotations[injectedAnnotation] == "true" {
		log.V(1).Info("Skipping already-injected pod")
		return nil
	}

	kind, name, err := p.resolveWorkload(ctx, pod)
	if err != nil {
		log.Error(err, "Failed to resolve ownerRef, skipping injection")
		return nil
	}

	if !p.hasInjectingPolicy(ctx, pod.Namespace, kind, name) {
		return nil
	}

	log.Info("Injecting Cairn agent")
	p.inject(pod)
	return nil
}

// resolveWorkload walks ownerReferences to find the pod's controlling workload.
// For Deployment-owned pods it follows the extra RS → Deployment hop.
func (p *PodInjector) resolveWorkload(ctx context.Context, pod *corev1.Pod) (kind, name string, err error) {
	for _, ref := range pod.OwnerReferences {
		if ref.Controller == nil || !*ref.Controller {
			continue
		}
		if ref.Kind != "ReplicaSet" {
			return ref.Kind, ref.Name, nil
		}
		// ReplicaSet → follow up to Deployment
		rs := &appsv1.ReplicaSet{}
		if err := p.Client.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: ref.Name}, rs); err != nil {
			return "", "", err
		}
		for _, rsRef := range rs.OwnerReferences {
			if rsRef.Controller != nil && *rsRef.Controller {
				return rsRef.Kind, rsRef.Name, nil
			}
		}
	}
	return "", "", nil // standalone pod
}

// hasInjectingPolicy returns true if any RightsizePolicy in the namespace
// targets this workload and has java.injectAgent enabled.
func (p *PodInjector) hasInjectingPolicy(ctx context.Context, namespace, workloadKind, workloadName string) bool {
	list := &rightsizingv1alpha1.RightsizePolicyList{}
	if err := p.Client.List(ctx, list, client.InNamespace(namespace)); err != nil {
		podInjectorLog.Error(err, "Failed to list RightsizePolicies, skipping injection")
		return false
	}
	for _, policy := range list.Items {
		java := policy.Spec.Java
		if java == nil || !java.Enabled || !java.InjectAgent {
			continue
		}
		ref := policy.Spec.TargetRef
		if ref.Kind != workloadKind {
			continue
		}
		if ref.Name == "*" || ref.Name == workloadName {
			return true
		}
	}
	return false
}

// inject mutates pod in-place to mount the agent image and configure JAVA_TOOL_OPTIONS.
// Uses ImageVolume (k8s 1.31+) — no init container or EmptyDir copy needed.
func (p *PodInjector) inject(pod *corev1.Pod) {
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: agentVolumeName,
		VolumeSource: corev1.VolumeSource{
			Image: &corev1.ImageVolumeSource{
				Reference:  p.AgentImage,
				PullPolicy: corev1.PullIfNotPresent,
			},
		},
	})

	agentFlag := "-javaagent:" + agentMountPath + "/agent.jar"

	for i := range pod.Spec.Containers {
		c := &pod.Spec.Containers[i]
		if !isJavaContainer(*c) {
			continue
		}

		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      agentVolumeName,
			MountPath: agentMountPath,
			ReadOnly:  true,
		})

		// Append to an existing JAVA_TOOL_OPTIONS rather than overwriting it.
		appended := false
		for j := range c.Env {
			if c.Env[j].Name == "JAVA_TOOL_OPTIONS" {
				c.Env[j].Value += " " + agentFlag
				appended = true
				break
			}
		}
		if !appended {
			c.Env = append(c.Env, corev1.EnvVar{
				Name:  "JAVA_TOOL_OPTIONS",
				Value: agentFlag,
			})
		}
	}

	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations[injectedAnnotation] = "true"
}
