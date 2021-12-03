package controllers

import (
	"time"

	capiv1alpha3 "sigs.k8s.io/cluster-api/api/v1alpha3"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	BlockClusterDeletion = "alpha.giantswarm.io/ignore-cluster-deletion"
)

func requeue() reconcile.Result {
	return ctrl.Result{
		Requeue:      true,
		RequeueAfter: time.Minute * 5,
	}
}

func getClusterCreationTimeStamp(cluster *capiv1alpha3.Cluster) time.Time {
	return cluster.CreationTimestamp.UTC()
}

func deletionApplied(cluster *capiv1alpha3.Cluster) bool {
	return cluster.DeletionTimestamp != nil
}

func deletionTimeReached(cluster *capiv1alpha3.Cluster) bool {
	return time.Now().UTC().After(getClusterCreationTimeStamp(cluster).Add(defaultTTL))
}

func deletionEventTimeReached(cluster *capiv1alpha3.Cluster) bool {
	return time.Now().UTC().After(getClusterCreationTimeStamp(cluster).Add(eventDefaultTTL))
}
