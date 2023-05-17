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
	"k8s.io/client-go/tools/record"
	capi "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ClusterReconciler reconciles a Cluster object
type ClusterReconciler struct {
	ctrlclient.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
	DryRun bool

	recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters/finalizers,verbs=update

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
	// ignore cluster deletion if timestamp is not nil or zero
	if !cluster.DeletionTimestamp.IsZero() {
		PendingTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		log.Info("Deletion for cluster is already applied")
		return ctrl.Result{}, nil
	}

	// ignore GitOps-managed resources
	if _, ok := cluster.Labels[fluxLabel]; ok {
		IgnoredTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		log.Info("Found label %s. Cluster will be ignored for deletion", fluxLabel)
		return ctrl.Result{}, nil
	}

	// ignore cluster from being deleted if ignore annotation is set
	if _, ok := cluster.Annotations[ignoreClusterDeletion]; ok {
		IgnoredTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		log.Info("Found annotation %s. Cluster will be ignored for deletion", ignoreClusterDeletion)
		return ctrl.Result{}, nil
	}

	// check if cluster has a keep-until label with a valid ISO date string
	if v, ok := cluster.Labels[keepUntil]; ok {
		t, err := time.Parse(keepUntilTimeLayout, v)
		if err != nil {
			ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
			log.Error(err, "failed to parse keep-until label value for cluster")
			return ctrl.Result{}, nil
		}
		if time.Now().UTC().Before(t) {
			IgnoredTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
			log.Info("Found label %s. Cluster will be ignored for deletion", keepUntil)
			return ctrl.Result{RequeueAfter: 24 * time.Hour}, nil
		}
	}

	// immediately delete the cluster if defaultTTL has passed
	if deletionTimeReached(cluster) {
		if !r.DryRun {
			// if it's a vintage cluster, we just try to remove the Cluster CR
			if _, ok := cluster.Labels[vintageReleaseVersion]; ok {
				err := deleteVintageCluster(ctx, log, r.Client, cluster)
				if err != nil {
					return ctrl.Result{}, err
				}
			} else {
				err := deleteClusterApp(ctx, log, r.Client, cluster)
				if err != nil {
					return ctrl.Result{}, err
				}
			}
		} else {
			log.Info("DryRun: skipping sending deletion event for cluster")
		}

		return ctrl.Result{}, nil
	}

	// only send marked for deletion event if we still have ~1h before the cluster gets deleted
	if deletionEventTimeReached(cluster) {
		if !r.DryRun {
			log.Info("Cluster is marked for deletion")
			r.submitClusterDeletionEvent(cluster, fmt.Sprintf("Cluster will be deleted in aprox. %v min.", deletionTime(cluster)))
		} else {
			log.Info("DryRun: skipping sending deletion event for cluster")
		}
		return ctrl.Result{
			RequeueAfter: 1 * time.Hour,
		}, nil
	}

	return requeue(), nil
}

func deleteVintageCluster(ctx context.Context, log logr.Logger, client ctrlclient.Client, cluster *capi.Cluster) error {
	log.Info("Cluster is being deleted", cluster.Namespace, cluster.Name)
	if err := client.Delete(ctx, cluster, ctrlclient.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
		log.Error(err, "unable to delete cluster")
		ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		return err
	}
	log.Info("Cluster was deleted")
	SuccessTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
	return nil
}

func deleteClusterApp(ctx context.Context, log logr.Logger, client ctrlclient.Client, cluster *capi.Cluster) error {
	// CAPI-based cluster but without Helm annotation? weird! should not happen; if do, we have log it
	if !hasChartAnnotations(cluster) {
		IgnoredTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		log.Info("Chart annotation not found for CAPI-based cluster. Cluster will be ignored for deletion")
		return nil
	}

	app := &gsapplication.App{}
	if err := client.Get(ctx, getClusterAppNamespacedName(cluster), app); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		log.Error(err, "Unable to get app CR for cluster")
		ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		return nil
	}
	// ignore GitOps-managed resources, ensure we're not deleting cluster app CR of MC itself
	if _, ok := app.Labels[fluxLabel]; ok {
		IgnoredTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		log.Info(fmt.Sprintf("Found label %s in App CR. Cluster will be ignored for deletion", fluxLabel))
		return nil
	}

	log.Info(fmt.Sprintf("Cluster has exceeded the default time to live (%s) and will be deleted", defaultTTL))

	// delete App CR for the cluster
	log.Info(fmt.Sprintf("App %s/%s is being deleted", app.Name, app.Namespace))
	if err := client.Delete(ctx, app, ctrlclient.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
		log.Error(err, "unable to delete App CR for cluster")
		ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		return err
	}
	log.Info(fmt.Sprintf("App %s/%s was deleted", app.Name, app.Namespace))

	// delete default-apps App CR for the cluster
	defaultApp := &gsapplication.App{}
	if err := client.Get(ctx, getDefaultAppNamespacedName(cluster), defaultApp); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		log.Error(err, "unable to get default-apps CR for cluster")
		ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		return err
	}
	log.Info(fmt.Sprintf("App %s/%s is being deleted", defaultApp.Name, defaultApp.Namespace))

	if err := client.Delete(ctx, defaultApp, ctrlclient.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
		log.Error(err, "unable to delete default-apps App CR for cluster")
		ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		return err
	}
	log.Info(fmt.Sprintf("App %s/%s was deleted", defaultApp.Name, defaultApp.Namespace))

	// delete config maps for the cluster
	cmSelector := labels.NewSelector()
	byClusterReq, _ := labels.NewRequirement(label.Cluster, selection.In, []string{cluster.Name})
	cmSelector = cmSelector.Add(*byClusterReq)
	propagationPolicy := metav1.DeletePropagationBackground
	if err := client.DeleteAllOf(ctx, &corev1.ConfigMap{}, &ctrlclient.DeleteAllOfOptions{
		ListOptions: ctrlclient.ListOptions{
			Namespace:     cluster.GetNamespace(),
			LabelSelector: cmSelector,
		},
		DeleteOptions: ctrlclient.DeleteOptions{
			PropagationPolicy: &propagationPolicy,
		},
	}); err != nil {
		log.Error(err, "unable to delete ConfigMaps for cluster")
		ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		return err
	}
	log.Info("Cluster configmaps was deleted")

	log.Info("Cluster was deleted")

	SuccessTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()

	return nil
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
