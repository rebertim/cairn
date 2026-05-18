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
	"slices"
	"strings"
	"time"

	rightsizingv1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	"github.com/sempex/cairn/internal/detector"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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

	policySpec := p.findPolicySpec(ctx, pod, kind, name)
	if policySpec == nil {
		return nil // no policy targets this workload
	}

	// Suspended is a kill-switch: skip all mutations so pods restart into their
	// original Deployment-spec state with no agent or resource overrides.
	if policySpec.Suspended {
		return nil
	}

	// Apply the latest recommendation to pod resources on creation only when the
	// policy is in auto mode. In dry-run/recommend mode the webhook must not
	// mutate resources so that the cluster state stays unaffected.
	if policySpec.Mode == "auto" {
		if rec := p.findRecommendation(ctx, pod.Namespace, kind, name); rec != nil {
			obsWindow := policySpec.MinObservationWindow.Duration
			if obsWindow == 0 {
				obsWindow = 24 * time.Hour
			}
			if rec.Status.DataReadySince == nil {
				log.V(1).Info("skipping recommendation — no data yet", "workload", name)
			} else if elapsed := time.Since(rec.Status.DataReadySince.Time); elapsed < obsWindow {
				log.V(1).Info("skipping recommendation — observation window not elapsed",
					"workload", name, "elapsed", elapsed, "required", obsWindow)
			} else {
				applyRecommendedResources(pod, rec)
				log.Info("applied recommendation to pod on creation", "workload", name)
			}
		}
	}

	javaEnabled := isJavaPod(pod) &&
		policySpec.Java != nil &&
		policySpec.Java.Enabled &&
		policySpec.Java.InjectAgent

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

// findPolicySpec returns the CommonPolicySpec for the first policy (namespace-
// scoped or cluster-scoped) that covers this workload, or nil if none exists.
// Namespace-scoped policies always take precedence over cluster policies.
func (p *PodInjector) findPolicySpec(ctx context.Context, pod *corev1.Pod, workloadKind, workloadName string) *rightsizingv1alpha1.CommonPolicySpec {
	// 1. Namespace-scoped policy takes precedence.
	nsList := &rightsizingv1alpha1.RightsizePolicyList{}
	if err := p.Client.List(ctx, nsList, client.InNamespace(pod.Namespace)); err != nil {
		podInjectorLog.Error(err, "Failed to list RightsizePolicies")
		return nil
	}
	for i := range nsList.Items {
		ref := nsList.Items[i].Spec.TargetRef
		if ref.Kind != workloadKind {
			continue
		}
		if ref.Name != "*" && ref.Name != "" && ref.Name != workloadName {
			continue
		}
		if ref.ContainerType == "java" && !isJavaPod(pod) {
			continue
		}
		if ref.ContainerType == "standard" && isJavaPod(pod) {
			continue
		}
		spec := nsList.Items[i].Spec.CommonPolicySpec
		return &spec
	}

	// 2. Fall back to any matching ClusterRightsizePolicy.
	cpList := &rightsizingv1alpha1.ClusterRightsizePolicyList{}
	if err := p.Client.List(ctx, cpList); err != nil {
		podInjectorLog.Error(err, "Failed to list ClusterRightsizePolicies")
		return nil
	}

	// Fetch the namespace object once for selector evaluation.
	nsObj := &corev1.Namespace{}
	if err := p.Client.Get(ctx, types.NamespacedName{Name: pod.Namespace}, nsObj); err != nil {
		podInjectorLog.Error(err, "Failed to get namespace", "namespace", pod.Namespace)
		return nil
	}

	for i := range cpList.Items {
		cp := &cpList.Items[i]
		if !cp.Spec.Enabled || cp.Spec.Suspended {
			continue
		}
		if cp.Spec.TargetRef.Kind != workloadKind {
			continue
		}
		// Exact name match or wildcard.
		isWildcard := cp.Spec.TargetRef.Name == "*" || cp.Spec.TargetRef.Name == ""
		isExact := !isWildcard && cp.Spec.TargetRef.Name == workloadName
		if !isWildcard && !isExact {
			continue
		}
		// Check namespace selector.
		if !clusterPolicyMatchesNamespace(cp.Spec.NamespaceSelector, nsObj) {
			continue
		}
		// Check container type filter.
		if cp.Spec.TargetRef.ContainerType == "java" && !isJavaPod(pod) {
			continue
		}
		if cp.Spec.TargetRef.ContainerType == "standard" && isJavaPod(pod) {
			continue
		}
		spec := cp.Spec.CommonPolicySpec
		return &spec
	}
	return nil
}

// clusterPolicyMatchesNamespace reports whether the given namespace passes the
// NamespaceSelector. A nil selector matches all namespaces.
func clusterPolicyMatchesNamespace(sel *rightsizingv1alpha1.NamespaceSelector, ns *corev1.Namespace) bool {
	if sel == nil {
		return true
	}
	if slices.Contains(sel.ExcludeNames, ns.Name) {
		return false
	}
	if len(sel.MatchNames) > 0 {
		return slices.Contains(sel.MatchNames, ns.Name)
	}
	if sel.LabelSelector != nil {
		selector, err := metav1.LabelSelectorAsSelector(sel.LabelSelector)
		if err != nil {
			return false
		}
		return selector.Matches(labels.Set(ns.Labels))
	}
	return true
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
		if cr.Recommended != nil && cr.Recommended.Limits != nil {
			if pod.Spec.Containers[i].Resources.Limits == nil {
				pod.Spec.Containers[i].Resources.Limits = make(corev1.ResourceList)
			}
			maps.Copy(pod.Spec.Containers[i].Resources.Limits, cr.Recommended.Limits)
		}
		if cr.Recommended != nil && cr.Recommended.Requests != nil {
			if pod.Spec.Containers[i].Resources.Requests == nil {
				pod.Spec.Containers[i].Resources.Requests = make(corev1.ResourceList)
			}
			maps.Copy(pod.Spec.Containers[i].Resources.Requests, cr.Recommended.Requests)
			clampRequestsToLimits(pod.Spec.Containers[i].Resources)
		}
		// Apply JVM flags if recommendation includes them (Java containers only).
		// inject() will have already appended -javaagent; updateJVMOpts preserves it.
		if cr.JVM != nil && cr.JVM.RecommendedFlags != nil {
			updated := updateJVMOpts(envValue(pod.Spec.Containers[i].Env), cr.JVM.RecommendedFlags)
			setEnvVar(&pod.Spec.Containers[i].Env, "JAVA_TOOL_OPTIONS", updated)
		}
	}
}

// markStandard annotates and labels the pod as a standard (non-Java) container
// so the controller routes it to the standard recommender and policies can
// select by container type via labelSelector.
func (p *PodInjector) markStandard(pod *corev1.Pod) {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations[containerTypeAnnotation] = containerTypeStandard
	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}
	pod.Labels[containerTypeAnnotation] = containerTypeStandard
}

// inject mutates pod in-place to mount the agent image and configure JAVA_TOOL_OPTIONS.
// Uses ImageVolume (k8s 1.31+) — no init container or EmptyDir copy needed.
func (p *PodInjector) inject(pod *corev1.Pod) {
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: agentVolumeName,
		VolumeSource: corev1.VolumeSource{
			Image: &corev1.ImageVolumeSource{
				Reference:  p.AgentImage,
				PullPolicy: corev1.PullAlways,
			},
		},
	})

	agentFlag := "-javaagent:" + agentMountPath + "/agent.jar"

	for i := range pod.Spec.Containers {
		c := &pod.Spec.Containers[i]
		if !detector.IsJavaContainer(*c) {
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
	pod.Labels[containerTypeAnnotation] = containerTypeJava
}

// updateJVMOpts strips any existing heap-sizing flags from the opts string and
// appends the new percentage-based values, preserving all other flags (e.g.
// -javaagent paths). Both old-style (-Xmx/-Xms) and new-style
// (-XX:MaxRAMPercentage / -XX:InitialRAMPercentage) are stripped for clean
// migration.
func updateJVMOpts(existing string, flags *rightsizingv1alpha1.JVMFlags) string {
	parts := strings.Fields(existing)
	filtered := parts[:0]
	for _, p := range parts {
		if strings.HasPrefix(p, "-Xmx") ||
			strings.HasPrefix(p, "-Xms") ||
			strings.HasPrefix(p, "-XX:MaxRAMPercentage=") ||
			strings.HasPrefix(p, "-XX:InitialRAMPercentage=") ||
			strings.HasPrefix(p, "-XX:MaxRAMFraction=") {
			continue
		}
		filtered = append(filtered, p)
	}
	if flags.MaxRAMPercentage != "" {
		filtered = append(filtered, "-XX:MaxRAMPercentage="+flags.MaxRAMPercentage)
	}
	if flags.InitialRAMPercentage != "" {
		filtered = append(filtered, "-XX:InitialRAMPercentage="+flags.InitialRAMPercentage)
	}
	return strings.Join(filtered, " ")
}

func envValue(env []corev1.EnvVar) string {
	for _, e := range env {
		if e.Name == "JAVA_TOOL_OPTIONS" {
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

// clampRequestsToLimits ensures no request exceeds its corresponding limit.
// Kubernetes rejects pods where request > limit, so we cap silently here.
func clampRequestsToLimits(res corev1.ResourceRequirements) {
	for name, limit := range res.Limits {
		if req, ok := res.Requests[name]; ok && req.Cmp(limit) > 0 {
			res.Requests[name] = limit.DeepCopy()
		}
	}
}
