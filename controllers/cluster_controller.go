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

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
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
	capi "sigs.k8s.io/cluster-api/api/core/v1beta2"
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
// +kubebuilder:rbac:groups=helm.toolkit.fluxcd.io,resources=helmreleases,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=ocirepositories,verbs=get;list;watch;delete

func (r *ClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("cluster", req.NamespacedName)

	// Fetch the Cluster instance.
	cluster := &capi.Cluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
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
		log.Info(fmt.Sprintf("Found label %s. Cluster will be ignored for deletion", fluxLabel))
		return ctrl.Result{}, nil
	}

	// ignore cluster from being deleted if ignore annotation is set
	if _, ok := cluster.Annotations[ignoreClusterDeletion]; ok {
		IgnoredTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		log.Info(fmt.Sprintf("Found annotation %s. Cluster will be ignored for deletion", ignoreClusterDeletion))
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
		if time.Now().UTC().Before(t.AddDate(0, 0, 1)) {
			IgnoredTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
			log.Info(fmt.Sprintf("Found label %s. Cluster will be ignored for deletion", keepUntil))
			return ctrl.Result{RequeueAfter: 24 * time.Hour}, nil
		}
	} else {
		// ignore cluster from being deleted if it is older than 7 days and do NOT have keep-until label
		// this is to prevent deletion in a case of accidental deployment of the app to production MCs
		if time.Since(cluster.CreationTimestamp.Time).Hours() > 24*7 {
			log.Info(fmt.Sprintf("Cluster is older than 7 days and does not have label %s. Cluster will be ignored for deletion", keepUntil))
			return ctrl.Result{}, nil
		}

	}

	// immediately delete the cluster if defaultTTL has passed
	if deletionTimeReached(cluster) {
		if !r.DryRun {
			// if it's a vintage cluster, we just try to remove the Cluster CR
			if _, ok := cluster.Labels[clusterOperatorVersion]; ok {
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
	log.Info("Cluster is being deleted")
	if err := client.Delete(ctx, cluster, ctrlclient.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
		log.Error(err, "unable to delete cluster")
		ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		return err
	}
	log.Info("Cluster was deleted")
	SuccessTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
	return nil
}

// deleteClusterApp tears down the release that provisions the cluster's workload,
// whether it's still App-operator managed (App CR) or has moved to being managed
// directly by Flux (HelmRelease). Both are looked up by the same Helm release
// name/namespace annotations on the Cluster, since either mechanism stamps them.
// TODO(cluster-cleaner): once every cluster has migrated off App CRs, drop the App path.
func deleteClusterApp(ctx context.Context, log logr.Logger, client ctrlclient.Client, cluster *capi.Cluster) error {
	// CAPI-based cluster but without Helm annotation? weird! should not happen; if do, we have log it
	if !hasChartAnnotations(cluster) {
		IgnoredTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		log.Info("Chart annotation not found for CAPI-based cluster. Cluster will be ignored for deletion")
		return nil
	}

	result, err := deleteRelease[gsapplication.App, *gsapplication.App](ctx, log, client, cluster, "App")
	if err != nil {
		return err
	}
	if result == releaseNotFound {
		// no App CR - the cluster's release may be managed by a Flux HelmRelease instead
		result, err = deleteRelease[helmv2.HelmRelease, *helmv2.HelmRelease](ctx, log, client, cluster, "HelmRelease")
		if err != nil {
			return err
		}
	}
	if result != releaseDeleted {
		return nil
	}

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
	log.Info("Cluster apps and configmaps were deleted")

	SuccessTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()

	return nil
}

// releaseResult is the outcome of trying to find and delete a cluster's release CR.
type releaseResult int

const (
	releaseNotFound releaseResult = iota // no CR of this kind found - caller may try a different kind
	releaseIgnored                       // CR is GitOps-managed, left alone
	releaseErrored                       // already logged and counted; caller should stop, no fallback
	releaseDeleted
)

// releasePtr constrains T to a struct whose pointer implements ctrlclient.Object,
// so deleteRelease can be instantiated for both App and HelmRelease.
type releasePtr[T any] interface {
	*T
	ctrlclient.Object
}

// deleteRelease looks up the release CR of kind T at the cluster's Helm release
// name/namespace annotations. It skips it if GitOps-managed (Flux Kustomize label),
// otherwise deletes it along with its "<cluster>-default-apps" counterpart of the
// same kind. The same rules apply regardless of kind - only the CR type differs.
func deleteRelease[T any, PT releasePtr[T]](ctx context.Context, log logr.Logger, c ctrlclient.Client, cluster *capi.Cluster, kind string) (releaseResult, error) {
	obj := PT(new(T))
	if err := c.Get(ctx, getClusterAppNamespacedName(cluster), obj); err != nil {
		if apierrors.IsNotFound(err) {
			return releaseNotFound, nil
		}
		log.Error(err, fmt.Sprintf("Unable to get %s for cluster", kind))
		ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		return releaseErrored, nil
	}

	// ignore GitOps-managed resources, ensure we're not deleting the MC's own release
	if _, ok := obj.GetLabels()[fluxLabel]; ok {
		IgnoredTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		log.Info(fmt.Sprintf("Found label %s in %s. Cluster will be ignored for deletion", fluxLabel, kind))
		return releaseIgnored, nil
	}

	log.Info(fmt.Sprintf("Cluster has exceeded the default time to live (%s) and will be deleted", defaultTTL))

	if err := deleteReleaseObj(ctx, log, c, cluster, kind, obj); err != nil {
		return releaseErrored, err
	}
	deleteChartRefOCIRepository(ctx, log, c, cluster, obj)

	// delete the default-apps counterpart for the cluster
	defaultObj := PT(new(T))
	if err := c.Get(ctx, getDefaultAppNamespacedName(cluster), defaultObj); err != nil {
		if apierrors.IsNotFound(err) {
			return releaseDeleted, nil
		}
		log.Error(err, fmt.Sprintf("unable to get default-apps %s for cluster", kind))
		ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		return releaseErrored, err
	}
	if err := deleteReleaseObj(ctx, log, c, cluster, kind, defaultObj); err != nil {
		return releaseErrored, err
	}
	deleteChartRefOCIRepository(ctx, log, c, cluster, defaultObj)

	return releaseDeleted, nil
}

// deleteChartRefOCIRepository best-effort deletes the OCIRepository a HelmRelease's
// spec.chartRef points to. It's a no-op for App CRs (which reference a shared Catalog,
// never an OCIRepository) and for HelmReleases without an OCIRepository chartRef.
// Any failure here is logged and counted but never fails the caller: the cluster's
// release has already been torn down by this point.
func deleteChartRefOCIRepository(ctx context.Context, log logr.Logger, c ctrlclient.Client, cluster *capi.Cluster, obj ctrlclient.Object) {
	hr, ok := obj.(*helmv2.HelmRelease)
	if !ok || hr.Spec.ChartRef == nil || hr.Spec.ChartRef.Kind != "OCIRepository" {
		return
	}

	key := ociRepositoryKey(hr.Spec.ChartRef, hr.Namespace)

	oci := &sourcev1.OCIRepository{}
	if err := c.Get(ctx, key, oci); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "unable to get OCIRepository for cluster")
			ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		}
		return
	}

	if _, ok := oci.GetLabels()[fluxLabel]; ok {
		log.Info(fmt.Sprintf("Found label %s in OCIRepository. It will be kept", fluxLabel))
		return
	}

	stillReferenced, err := ociRepositoryStillReferenced(ctx, c, hr, key)
	if err != nil {
		log.Error(err, "unable to check whether OCIRepository is still referenced")
		ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		return
	}
	if stillReferenced {
		log.Info(fmt.Sprintf("OCIRepository %s/%s is still referenced by another HelmRelease. It will be kept", key.Namespace, key.Name))
		return
	}

	if err := c.Delete(ctx, oci, ctrlclient.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
		log.Error(err, "unable to delete OCIRepository for cluster")
		ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		return
	}
	log.Info(fmt.Sprintf("OCIRepository %s/%s was deleted", oci.GetNamespace(), oci.GetName()))
}

// ociRepositoryStillReferenced lists all HelmReleases cluster-wide and checks whether
// any HelmRelease other than the one being deleted still points its chartRef at the
// given OCIRepository, so we don't delete a chart shared by another cluster's release.
func ociRepositoryStillReferenced(ctx context.Context, c ctrlclient.Client, deleting *helmv2.HelmRelease, ociKey ctrlclient.ObjectKey) (bool, error) {
	var list helmv2.HelmReleaseList
	if err := c.List(ctx, &list); err != nil {
		return false, err
	}

	for i := range list.Items {
		other := &list.Items[i]
		if other.Name == deleting.Name && other.Namespace == deleting.Namespace {
			continue
		}
		if other.Spec.ChartRef == nil || other.Spec.ChartRef.Kind != "OCIRepository" {
			continue
		}
		if ociRepositoryKey(other.Spec.ChartRef, other.Namespace) == ociKey {
			return true, nil
		}
	}

	return false, nil
}

// ociRepositoryKey resolves the namespaced name a chartRef points at, defaulting the
// namespace to the owning HelmRelease's own when the reference leaves it unset.
func ociRepositoryKey(ref *helmv2.CrossNamespaceSourceReference, ownerNamespace string) ctrlclient.ObjectKey {
	namespace := ref.Namespace
	if namespace == "" {
		namespace = ownerNamespace
	}
	return ctrlclient.ObjectKey{Name: ref.Name, Namespace: namespace}
}

func deleteReleaseObj(ctx context.Context, log logr.Logger, c ctrlclient.Client, cluster *capi.Cluster, kind string, obj ctrlclient.Object) error {
	log.Info(fmt.Sprintf("%s %s/%s is being deleted", kind, obj.GetName(), obj.GetNamespace()))
	if err := c.Delete(ctx, obj, ctrlclient.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
		log.Error(err, fmt.Sprintf("unable to delete %s for cluster", kind))
		ErrorsTotal.WithLabelValues(cluster.Name, cluster.Namespace).Inc()
		return err
	}
	log.Info(fmt.Sprintf("%s %s/%s was deleted", kind, obj.GetName(), obj.GetNamespace()))
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
	r.recorder.Eventf(cluster, corev1.EventTypeNormal, "ClusterMarkedForDeletion", "%s", message)
}
