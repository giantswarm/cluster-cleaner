package controllers

import (
	"time"

	capiv1alpha3 "sigs.k8s.io/cluster-api/api/v1alpha3"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ignoreClusterDeletion = "alpha.giantswarm.io/ignore-cluster-deletion"

	// defaultTTL is the default time to live for a cluster.
	defaultTTL = 8 * time.Hour

	// eventDefaultTTL is the default time when we sent a `ClusterMarkedForDeletion` event.
	eventDefaultTTL = defaultTTL - 1*time.Hour
)

func requeue() reconcile.Result {
	return ctrl.Result{
		RequeueAfter: time.Minute * 5,
	}
}

func getClusterCreationTimeStamp(cluster *capiv1alpha3.Cluster) time.Time {
	return cluster.CreationTimestamp.UTC()
}

func deletionTimeReached(cluster *capiv1alpha3.Cluster) bool {
	return time.Now().UTC().After(getClusterCreationTimeStamp(cluster).Add(defaultTTL))
}

func deletionEventTimeReached(cluster *capiv1alpha3.Cluster) bool {
	return time.Now().UTC().After(getClusterCreationTimeStamp(cluster).Add(eventDefaultTTL))
}
