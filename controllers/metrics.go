package controllers

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	metricNamespace = "cluster_cleaner"
	metricSubsystem = "cluster"
)

// Counters for cluster deletions
var (
	counterLabels = []string{"cluster_id", "cluster_namespace"}

	IgnoredTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "deletion_ignored_total",
			Help:      "Number of all ignored cluster deletion",
		},
		counterLabels,
	)
	PendingTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "deletion_pending_total",
			Help:      "Number of all pending cluster deletion",
		},
		counterLabels,
	)
	ErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "deletion_errors_total",
			Help:      "Number of all failed cluster deletion",
		},
		counterLabels,
	)
	SuccessTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "deletion_succeeded_total",
			Help:      "Number of all clusters that were deleted successfully",
		},
		counterLabels,
	)
)

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(PendingTotal, ErrorsTotal, SuccessTotal, IgnoredTotal)
}
