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

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	rightsizingv1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	"github.com/sempex/cairn/internal/actuator"
)

var _ = Describe("RightsizeRecommendation Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName = "test-resource"
			policyName   = "test-policy"
			namespace    = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: namespace,
		}

		BeforeEach(func() {
			By("creating the RightsizePolicy")
			policy := &rightsizingv1alpha1.RightsizePolicy{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: policyName, Namespace: namespace}, policy)
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, &rightsizingv1alpha1.RightsizePolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      policyName,
						Namespace: namespace,
					},
					Spec: rightsizingv1alpha1.RightsizePolicySpec{
						TargetRef: rightsizingv1alpha1.TargetRef{Kind: "Deployment", Name: "*"},
					},
				})).To(Succeed())
			}

			By("creating the RightsizeRecommendation")
			rec := &rightsizingv1alpha1.RightsizeRecommendation{}
			err = k8sClient.Get(ctx, typeNamespacedName, rec)
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, &rightsizingv1alpha1.RightsizeRecommendation{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: namespace,
					},
					Spec: rightsizingv1alpha1.RightsizeRecommendationSpec{
						TargetRef: rightsizingv1alpha1.TargetRef{Kind: "Deployment", Name: "my-app"},
						PolicyRef: rightsizingv1alpha1.PolicyReference{
							Kind:      "RightsizePolicy",
							Name:      policyName,
							Namespace: namespace,
						},
					},
				})).To(Succeed())
			}
		})

		AfterEach(func() {
			rec := &rightsizingv1alpha1.RightsizeRecommendation{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, rec)).To(Succeed())
			By("Cleanup the specific resource instance RightsizeRecommendation")
			Expect(k8sClient.Delete(ctx, rec)).To(Succeed())

			policy := &rightsizingv1alpha1.RightsizePolicy{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: policyName, Namespace: namespace}, policy)).To(Succeed())
			By("Cleanup the RightsizePolicy")
			Expect(k8sClient.Delete(ctx, policy)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &RightsizeRecommendationReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Engine: actuator.NewEngine(
					actuator.NewDryRunActuator(),
					actuator.NewInPlaceActuator(k8sClient),
					actuator.NewRestartActuator(k8sClient),
				),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
