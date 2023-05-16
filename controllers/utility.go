package controllers

import (
	"fmt"
	"time"

	capi "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ignoreClusterDeletion = "alpha.giantswarm.io/ignore-cluster-deletion"
	keepUntil             = "keep-until"

	// defaultTTL is the default time to live for a cluster.
	defaultTTL = 4 * time.Hour

	// eventDefaultTTL is the default time when we sent a `ClusterMarkedForDeletion` event.
	eventDefaultTTL = defaultTTL - 1*time.Hour

	// keepUntilTimeLayout is the layout for the `keep-until` label.
	keepUntilTimeLayout = "2006-01-02"

	// helmReleaseNameAnnotation is the annotation containing the chart release name
	helmReleaseNameAnnotation = "meta.helm.sh/release-name"

	// helmReleaseNamespaceAnnotation is the annotation containing the chart release namespace
	helmReleaseNamespaceAnnotation = "meta.helm.sh/release-namespace"

	// fluxLabel is the label for checking if the cluster is created via git-ops
	fluxLabel = "kustomize.toolkit.fluxcd.io/name"

	vintageReleaseVersion = "release.giantswarm.io/version"
)

func requeue() reconcile.Result {
	return ctrl.Result{
		RequeueAfter: time.Minute * 5,
	}
}

func getClusterCreationTimeStamp(cluster *capi.Cluster) time.Time {
	return cluster.CreationTimestamp.UTC()
}

func deletionTimeReached(cluster *capi.Cluster) bool {
	return time.Now().UTC().After(getClusterCreationTimeStamp(cluster).Add(defaultTTL))
}

func deletionTime(cluster *capi.Cluster) int {
	return int(defaultTTL.Minutes()) - int(time.Now().UTC().Sub(getClusterCreationTimeStamp(cluster)).Minutes())
}

func deletionEventTimeReached(cluster *capi.Cluster) bool {
	return time.Now().UTC().After(getClusterCreationTimeStamp(cluster).Add(eventDefaultTTL))
}

func hasChartAnnotations(cluster *capi.Cluster) bool {
	releaseName, nameOK := cluster.ObjectMeta.Annotations[helmReleaseNameAnnotation]
	releaseNamespace, namespaceOK := cluster.ObjectMeta.Annotations[helmReleaseNamespaceAnnotation]
	return nameOK && namespaceOK && releaseName != "" && releaseNamespace != ""
}

func getClusterAppNamespacedName(cluster *capi.Cluster) client.ObjectKey {
	return client.ObjectKey{
		Name:      cluster.ObjectMeta.Annotations[helmReleaseNameAnnotation],
		Namespace: cluster.ObjectMeta.Annotations[helmReleaseNamespaceAnnotation],
	}
}

func getDefaultAppNamespacedName(cluster *capi.Cluster) client.ObjectKey {
	return client.ObjectKey{
		Name:      fmt.Sprintf("%s-default-apps", cluster.ObjectMeta.Annotations[helmReleaseNameAnnotation]),
		Namespace: cluster.ObjectMeta.Annotations[helmReleaseNamespaceAnnotation],
	}
}
