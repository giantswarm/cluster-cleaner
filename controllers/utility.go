package controllers

import (
	"time"

	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ignoreClusterDeletion = "alpha.giantswarm.io/ignore-cluster-deletion"
	keepUntil             = "keep-until"

	// defaultTTL is the default time to live for a cluster.
	defaultTTL = 10 * time.Hour

	// eventDefaultTTL is the default time when we sent a `ClusterMarkedForDeletion` event.
	eventDefaultTTL = defaultTTL - 1*time.Hour

	// keepUntilTimeLayout is the layout for the `keep-until` label.
	keepUntilTimeLayout = "2006-01-02"

	// helmReleaseNameAnnotation is the annotation containing the chart release name
	helmReleaseNameAnnotation = "meta.helm.sh/release-name"

	// helmReleaseNamespaceAnnotation is the annotation containing the chart release namespace
	helmReleaseNamespaceAnnotation = "meta.helm.sh/release-namespace"
)

func requeue() reconcile.Result {
	return ctrl.Result{
		RequeueAfter: time.Minute * 5,
	}
}

func getClusterCreationTimeStamp(cluster *capiv1beta1.Cluster) time.Time {
	return cluster.CreationTimestamp.UTC()
}

func deletionTimeReached(cluster *capiv1beta1.Cluster) bool {
	return time.Now().UTC().After(getClusterCreationTimeStamp(cluster).Add(defaultTTL))
}

func deletionTime(cluster *capiv1beta1.Cluster) int {
	return int(defaultTTL.Minutes()) - int(time.Now().UTC().Sub(getClusterCreationTimeStamp(cluster)).Minutes())
}

func deletionEventTimeReached(cluster *capiv1beta1.Cluster) bool {
	return time.Now().UTC().After(getClusterCreationTimeStamp(cluster).Add(eventDefaultTTL))
}

func hasChartAnnotations(cluster *capiv1beta1.Cluster) bool {
	releaseName, nameOK := cluster.ObjectMeta.Annotations[helmReleaseNameAnnotation]
	releaseNamespace, namespaceOK := cluster.ObjectMeta.Annotations[helmReleaseNamespaceAnnotation]
	return nameOK && namespaceOK && releaseName != "" && releaseNamespace != ""
}

func getChartNamespacedName(cluster *capiv1beta1.Cluster) client.ObjectKey {
	return client.ObjectKey{
		Name:      cluster.ObjectMeta.Annotations[helmReleaseNameAnnotation],
		Namespace: cluster.ObjectMeta.Annotations[helmReleaseNamespaceAnnotation],
	}
}
