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

package webhook

import (
	"context"
	"maps"
	"strings"

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
	injectedAnnotation      = "cairn.io/agent-injected"
	injectedValue           = "true"
	containerTypeAnnotation = "cairn.io/container-type"
	containerTypeJava       = "java"
	containerTypeStandard   = "standard"
	agentVolumeName         = "cairn-agent"
	agentMountPath          = "/cairn"
	agentMetricsPort        = 9404
	agentPortName           = "cairn-metrics"
)

// +kubebuilder:webhook:path=/mutate--v1-pod,mutating=true,failurePolicy=ignore,sideEffects=None,groups="",resources=pods,verbs=create,versions=v1,name=mpod.cairn.io,admissionReviewVersions=v1
// +kubebuilder:rbac:groups=apps,resources=replicasets,verbs=get;list;watch
// +kubebuilder:rbac:groups=rightsizing.cairn.io,resources=rightsizepolicies,verbs=list
// +kubebuilder:rbac:groups=rightsizing.cairn.io,resources=rightsizerecommendations,verbs=get

// PodInjector injects the Cairn JVM agent into Java pods and applies the latest
// resource recommendation to every pod at creation time, preventing drift when
// pods restart and the Deployment spec would otherwise revert the resources.
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

	// Skip if already processed by this webhook.
	if pod.Annotations[containerTypeAnnotation] != "" {
		log.V(1).Info("Skipping already-annotated pod")
		return nil
	}

	kind, name, err := p.resolveWorkload(ctx, pod)
	if err != nil {
		log.Error(err, "Failed to resolve ownerRef, skipping")
		return nil
	}
	if name == "" {
		return nil
	}

	policy := p.findPolicy(ctx, pod.Namespace, kind, name)
	if policy == nil {
		return nil // no policy targets this workload
	}

	// Suspended is a kill-switch: skip all mutations so pods restart into their
	// original Deployment-spec state with no agent or resource overrides.
	if policy.Spec.Suspended {
		return nil
	}

	// Apply the latest recommendation to pod resources on creation.
	// This prevents drift for inplace strategy (where the Deployment spec is never
	// updated) and ensures restart strategy pods start with the correct resources
	// before the first request is served.
	if rec := p.findRecommendation(ctx, pod.Namespace, kind, name); rec != nil {
		applyRecommendedResources(pod, rec)
		log.Info("applied recommendation to pod on creation", "workload", name)
	}

	javaEnabled := isJavaPod(pod) &&
		policy.Spec.Java != nil &&
		policy.Spec.Java.Enabled &&
		policy.Spec.Java.InjectAgent

	if javaEnabled {
		log.Info("Injecting Cairn agent")
		p.inject(pod)
	} else {
		log.Info("Marking pod as standard")
		p.markStandard(pod)
	}
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

// findPolicy returns the first RightsizePolicy in the namespace that targets
// this workload, or nil if none exists.
func (p *PodInjector) findPolicy(ctx context.Context, namespace, workloadKind, workloadName string) *rightsizingv1alpha1.RightsizePolicy {
	list := &rightsizingv1alpha1.RightsizePolicyList{}
	if err := p.Client.List(ctx, list, client.InNamespace(namespace)); err != nil {
		podInjectorLog.Error(err, "Failed to list RightsizePolicies")
		return nil
	}
	for i := range list.Items {
		ref := list.Items[i].Spec.TargetRef
		if ref.Kind != workloadKind {
			continue
		}
		if ref.Name == "*" || ref.Name == workloadName {
			return &list.Items[i]
		}
	}
	return nil
}

// findRecommendation looks up the RightsizeRecommendation for the workload, or
// returns nil if it does not exist yet.
func (p *PodInjector) findRecommendation(ctx context.Context, namespace, kind, name string) *rightsizingv1alpha1.RightsizeRecommendation {
	rec := &rightsizingv1alpha1.RightsizeRecommendation{}
	recName := strings.ToLower(kind) + "-" + name
	if err := p.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: recName}, rec); err != nil {
		return nil
	}
	return rec
}

// applyRecommendedResources patches the pod's container resources and JVM flags
// from the recommendation status. Only keys present in the recommendation are
// updated; others are left untouched.
func applyRecommendedResources(pod *corev1.Pod, rec *rightsizingv1alpha1.RightsizeRecommendation) {
	idx := make(map[string]rightsizingv1alpha1.ContainerRecommendation, len(rec.Status.Containers))
	for _, c := range rec.Status.Containers {
		idx[c.ContainerName] = c
	}
	for i := range pod.Spec.Containers {
		cr, ok := idx[pod.Spec.Containers[i].Name]
		if !ok {
			continue
		}
		if cr.Recommended != nil && cr.Recommended.Requests != nil {
			if pod.Spec.Containers[i].Resources.Requests == nil {
				pod.Spec.Containers[i].Resources.Requests = make(corev1.ResourceList)
			}
			maps.Copy(pod.Spec.Containers[i].Resources.Requests, cr.Recommended.Requests)
		}
		if cr.Recommended != nil && cr.Recommended.Limits != nil {
			if pod.Spec.Containers[i].Resources.Limits == nil {
				pod.Spec.Containers[i].Resources.Limits = make(corev1.ResourceList)
			}
			maps.Copy(pod.Spec.Containers[i].Resources.Limits, cr.Recommended.Limits)
		}
		// Apply JVM flags if recommendation includes them (Java containers only).
		// inject() will have already appended -javaagent; updateJVMOpts preserves it.
		if cr.JVM != nil && cr.JVM.RecommendedFlags != nil {
			updated := updateJVMOpts(envValue(pod.Spec.Containers[i].Env, "JAVA_TOOL_OPTIONS"), cr.JVM.RecommendedFlags)
			setEnvVar(&pod.Spec.Containers[i].Env, "JAVA_TOOL_OPTIONS", updated)
		}
	}
}

// markStandard annotates the pod as a standard (non-Java) container so the
// controller routes it to the standard recommender.
func (p *PodInjector) markStandard(pod *corev1.Pod) {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations[containerTypeAnnotation] = containerTypeStandard
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

		if !hasPort(c.Ports, agentMetricsPort) {
			c.Ports = append(c.Ports, corev1.ContainerPort{
				Name:          agentPortName,
				ContainerPort: agentMetricsPort,
				Protocol:      corev1.ProtocolTCP,
			})
		}

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
	pod.Annotations[injectedAnnotation] = injectedValue
	pod.Annotations[containerTypeAnnotation] = containerTypeJava

	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}
	pod.Labels[injectedAnnotation] = injectedValue
}

// updateJVMOpts strips any existing -Xmx/-Xms from the opts string and appends
// the new values, preserving all other flags (e.g. -javaagent paths).
func updateJVMOpts(existing string, flags *rightsizingv1alpha1.JVMFlags) string {
	parts := strings.Fields(existing)
	filtered := parts[:0]
	for _, p := range parts {
		if !strings.HasPrefix(p, "-Xmx") && !strings.HasPrefix(p, "-Xms") {
			filtered = append(filtered, p)
		}
	}
	if flags.Xmx != "" {
		filtered = append(filtered, "-Xmx"+flags.Xmx)
	}
	if flags.Xms != "" {
		filtered = append(filtered, "-Xms"+flags.Xms)
	}
	return strings.Join(filtered, " ")
}

func envValue(env []corev1.EnvVar, name string) string {
	for _, e := range env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}

func setEnvVar(env *[]corev1.EnvVar, name, value string) {
	for i := range *env {
		if (*env)[i].Name == name {
			(*env)[i].Value = value
			return
		}
	}
	*env = append(*env, corev1.EnvVar{Name: name, Value: value})
}

func hasPort(ports []corev1.ContainerPort, port int32) bool {
	for _, p := range ports {
		if p.ContainerPort == port {
			return true
		}
	}
	return false
}
