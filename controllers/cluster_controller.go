/*
Copyright 2021.

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

package controllers

import (
	"context"
	"fmt"
	"time"

	gsapplication "github.com/giantswarm/apiextensions-application/api/v1alpha1"
	"github.com/giantswarm/k8smetadata/pkg/label"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/tools/record"
	capi "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ClusterAppsOperator ConfigMap
type Provider struct {
	Kind string `yaml:"kind"`
}
type Service struct {
	Provider Provider `yaml:"provider"`
}
type ClusterAppsConfig struct {
	Service Service `yaml:"service"`
}

// ClusterReconciler reconciles a Cluster object
type ClusterReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
	DryRun bool

	recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters/finalizers,verbs=update

func (r *ClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("cluster", req.NamespacedName)

	// Fetch the Cluster instance.
	cluster := &capi.Cluster{}
	if err := r.Client.Get(ctx, req.NamespacedName, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	return r.reconcile(ctx, cluster, log)
}

func (r *ClusterReconciler) reconcile(ctx context.Context, cluster *capi.Cluster, log logr.Logger) (ctrl.Result, error) {
	err, provider := r.getClusterProvider(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	log.Info(fmt.Sprintf("Cluster %s/%s provider is %s", cluster.Namespace, cluster.Name, provider))

	// ignore GitOps-managed resources, ensure MC itself doesn't commit suicide
	if _, ok := cluster.Labels[fluxLabel]; ok {
		IgnoredTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		log.Info(fmt.Sprintf("Found label %s. Cluster %s/%s will be ignored for deletion", fluxLabel, cluster.Namespace, cluster.Name))
		return ctrl.Result{}, nil
	}

	// ignore cluster from being deleted if ignore annotation is set
	if _, ok := cluster.Annotations[ignoreClusterDeletion]; ok {
		IgnoredTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		log.Info(fmt.Sprintf("Found annotation %s. Cluster %s/%s will be ignored for deletion", ignoreClusterDeletion, cluster.Namespace, cluster.Name))
		return ctrl.Result{}, nil
	}

	// check if cluster has a keep-until label with a valid ISO date string
	if v, ok := cluster.Labels[keepUntil]; ok {
		t, err := time.Parse(keepUntilTimeLayout, v)
		if err != nil {
			ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
			log.Error(err, fmt.Sprintf("failed to parse keep-until label value for cluster %s/%s", cluster.Namespace, cluster.Name))
			return ctrl.Result{}, nil
		}
		if time.Now().UTC().Before(t) {
			IgnoredTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
			log.Info(fmt.Sprintf("Found label %s. Cluster %s/%s will be ignored for deletion", keepUntil, cluster.Namespace, cluster.Name))
			return ctrl.Result{RequeueAfter: 24 * time.Hour}, nil
		}
	}

	// ignore cluster deletion if timestamp is not nil or zero
	if !cluster.DeletionTimestamp.IsZero() {
		PendingTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		log.Info(fmt.Sprintf("Deletion for cluster %s/%s is already applied", cluster.Namespace, cluster.Name))
		return ctrl.Result{}, nil
	}

	// immediately delete the cluster if defaultTTL has passed
	if deletionTimeReached(cluster) {
		propagationPolicy := metav1.DeletePropagationBackground

		if !r.DryRun {
			// vintage cluster
			if provider == "aws" {
				log.Info(fmt.Sprintf("Cluster %s/%s is being deleted", cluster.Namespace, cluster.Name))
				if err := r.Client.Delete(ctx, cluster, client.PropagationPolicy(propagationPolicy)); err != nil {
					log.Error(err, fmt.Sprintf("unable to delete cluster %s/%s", cluster.Namespace, cluster.Name))
					ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
					return requeue(), nil
				}
				log.Info(fmt.Sprintf("Cluster %s/%s was deleted", cluster.Namespace, cluster.Name))
				SuccessTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
				return ctrl.Result{}, nil
			}
			// CAPI-based cluster but without Helm annotation? weird! should not happen; if do, we have log it
			if !hasChartAnnotations(cluster) {
				IgnoredTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
				log.Info(fmt.Sprintf("Chart annotation not found for CAPI-based cluster. Cluster %s/%s will be ignored for deletion", cluster.Namespace, cluster.Name))
				return ctrl.Result{}, nil
			}
			app := &gsapplication.App{}
			if err := r.Client.Get(ctx, getClusterAppNamespacedName(cluster), app); err != nil {
				if apierrors.IsNotFound(err) {
					return ctrl.Result{}, nil
				}
				log.Error(err, fmt.Sprintf("unable to get app CR for cluster %s/%s", cluster.Namespace, cluster.Name))
				ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
				return requeue(), nil
			}
			// ignore GitOps-managed resources, ensure we're not deleting cluster app CR of MC itself
			if _, ok := app.Labels[fluxLabel]; ok {
				IgnoredTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
				log.Info(fmt.Sprintf("Found label %s in App CR. Cluster %s/%s will be ignored for deletion", fluxLabel, cluster.Namespace, cluster.Name))
				return ctrl.Result{}, nil
			}

			log.Info(fmt.Sprintf("Cluster %s/%s has exceeded the default time to live (%s) and will be deleted", cluster.Namespace, cluster.Name, defaultTTL))

			// delete App CR for the cluster
			log.Info(fmt.Sprintf("App %s/%s is being deleted", app.Name, app.Namespace))
			if err := r.Client.Delete(ctx, app, client.PropagationPolicy(propagationPolicy)); err != nil {
				log.Error(err, fmt.Sprintf("unable to delete App CR for cluster %s/%s", cluster.Namespace, cluster.Name))
				ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
				return requeue(), nil
			}
			log.Info(fmt.Sprintf("App %s/%s was deleted", app.Name, app.Namespace))

			// delete default-apps App CR for the cluster
			defaultApp := &gsapplication.App{}
			if err := r.Client.Get(ctx, getDefaultAppNamespacedName(cluster), defaultApp); err != nil {
				if apierrors.IsNotFound(err) {
					return ctrl.Result{}, nil
				}
				log.Error(err, fmt.Sprintf("unable to get default-apps CR for cluster %s/%s", cluster.Namespace, cluster.Name))
				ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
				return requeue(), nil
			}
			log.Info(fmt.Sprintf("App %s/%s is being deleted", defaultApp.Name, defaultApp.Namespace))

			if err := r.Client.Delete(ctx, defaultApp, client.PropagationPolicy(propagationPolicy)); err != nil {
				log.Error(err, fmt.Sprintf("unable to delete default-apps App CR for cluster %s/%s", cluster.Namespace, cluster.Name))
				ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
				return requeue(), nil
			}
			log.Info(fmt.Sprintf("App %s/%s was deleted", defaultApp.Name, defaultApp.Namespace))

			// delete config maps for the cluster
			cmSelector := labels.NewSelector()
			byClusterReq, _ := labels.NewRequirement(label.Cluster, selection.In, []string{cluster.Name})
			cmSelector = cmSelector.Add(*byClusterReq)
			if err := r.Client.DeleteAllOf(ctx, &corev1.ConfigMap{}, &client.DeleteAllOfOptions{
				ListOptions: client.ListOptions{
					Namespace:     cluster.GetNamespace(),
					LabelSelector: cmSelector,
				},
				DeleteOptions: client.DeleteOptions{
					PropagationPolicy: &propagationPolicy,
					DryRun:            []string{"All"},
				},
			}); err != nil {
				log.Error(err, fmt.Sprintf("unable to delete ConfigMaps for cluster %s/%s", cluster.Namespace, cluster.Name))
				ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
				return requeue(), nil
			}
			log.Info(fmt.Sprintf("Cluster %s/%s configmaps was deleted", cluster.Namespace, cluster.Name))

			log.Info(fmt.Sprintf("Cluster %s/%s was deleted", cluster.Namespace, cluster.Name))
			SuccessTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		} else {
			log.Info(fmt.Sprintf("DryRun: skipping sending deletion event for cluster %s/%s", cluster.Namespace, cluster.Name))
		}

		return ctrl.Result{}, nil
	}

	// only send marked for deletion event if we still have ~1h before the cluster gets deleted
	if deletionEventTimeReached(cluster) {
		if !r.DryRun {
			log.Info(fmt.Sprintf("Cluster %s/%s is marked for deletion", cluster.Namespace, cluster.Name))
			r.submitClusterDeletionEvent(cluster, fmt.Sprintf("Cluster %s/%s will be deleted in aprox. %v min.", cluster.Namespace, cluster.Name, deletionTime(cluster)))
		} else {
			log.Info(fmt.Sprintf("DryRun: skipping sending deletion event for cluster %s/%s", cluster.Namespace, cluster.Name))
		}
		return ctrl.Result{
			RequeueAfter: 1 * time.Hour,
		}, nil
	}

	return requeue(), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(&capi.Cluster{}).
		Complete(r)
	if err != nil {
		return errors.Wrap(err, "failed setting up with a controller manager")
	}

	r.recorder = mgr.GetEventRecorderFor("cluster-controller")
	return nil
}

func (r *ClusterReconciler) submitClusterDeletionEvent(cluster *capi.Cluster, message string) {
	r.recorder.Eventf(cluster, corev1.EventTypeNormal, "ClusterMarkedForDeletion", message)
}

func (r *ClusterReconciler) getClusterProvider(ctx context.Context) (error, string) {
	var c ClusterAppsConfig
	cm := &corev1.ConfigMap{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      "cluster-apps-operator",
		Namespace: "giantswarm",
	}, cm)
	if err != nil && !apierrors.IsNotFound(err) {
		return err, ""
	}
	data := cm.Data["config.yaml"]
	err = yaml.Unmarshal([]byte(data), &c)
	if err != nil {
		return err, ""
	}
	return nil, c.Service.Provider.Kind
}
