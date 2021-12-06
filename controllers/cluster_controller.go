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

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	capiv1alpha3 "sigs.k8s.io/cluster-api/api/v1alpha3"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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
	cluster := &capiv1alpha3.Cluster{}
	if err := r.Client.Get(ctx, req.NamespacedName, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	return r.reconcile(ctx, cluster, log)
}

func (r *ClusterReconciler) reconcile(ctx context.Context, cluster *capiv1alpha3.Cluster, log logr.Logger) (ctrl.Result, error) {
	if _, ok := cluster.Annotations[ignoreClusterDeletion]; ok {
		log.Info(fmt.Sprintf("Found annotation %s. Cluster %s/%s will be ignored for deletion", ignoreClusterDeletion, cluster.Namespace, cluster.Name))
		return ctrl.Result{}, nil

	}

	if !cluster.DeletionTimestamp.IsZero() {
		log.Info(fmt.Sprintf("Deletion for cluster %s/%s is already applied", cluster.Namespace, cluster.Name))
		return ctrl.Result{}, nil
	}

	// immediately delete the cluster if defaultTTL has passed
	if deletionTimeReached(cluster) {
		if !r.DryRun {
			err := r.Client.Delete(ctx, cluster, client.PropagationPolicy(metav1.DeletePropagationBackground))
			if err != nil {
				log.Error(err, fmt.Sprintf("unable to delete cluster %s/%s", cluster.Namespace, cluster.Name))
				return requeue(), nil
			}
			log.Info(fmt.Sprintf("Cluster %s/%s will be deleted", cluster.Namespace, cluster.Name))
		} else {
			log.Info("DryRun: skipping sending deletion event for cluster %s/%s", cluster.Namespace, cluster.Name)

		}

		return ctrl.Result{}, nil
	}

	// only send marked for deletion event if we still have ~1h before the cluster gets deleted
	if deletionEventTimeReached(cluster) {
		if !r.DryRun {
			log.Info(fmt.Sprintf("Cluster %s/%s is marked for deletion", cluster.Namespace, cluster.Name))
			r.submitClusterDeletionEvent(cluster, fmt.Sprintf("Cluster %s/%s will be deleted in aprox. %v min.", cluster.Namespace, cluster.Name, deletionTime(cluster)))
		} else {
			log.Info("DryRun: skipping sending deletion event for cluster %s/%s", cluster.Namespace, cluster.Name)
		}
		return ctrl.Result{
			RequeueAfter: 1 * time.Hour,
		}, nil
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(&capiv1alpha3.Cluster{}).
		Complete(r)
	if err != nil {
		return errors.Wrap(err, "failed setting up with a controller manager")
	}

	r.recorder = mgr.GetEventRecorderFor("cluster-controller")
	return nil
}

func (r *ClusterReconciler) submitClusterDeletionEvent(cluster *capiv1alpha3.Cluster, message string) {
	r.recorder.Eventf(cluster, corev1.EventTypeNormal, "ClusterMarkedForDeletion", message)
}
