package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Metrics emitted on the controller-runtime /metrics endpoint. Cardinality
// is intentionally low: only namespace + name + an outcome bucket — no
// per-resource labels — so dashboards aggregate cleanly.
var (
	reconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bigbang_operator_reconcile_total",
			Help: "Total Package reconciliations completed, partitioned by outcome.",
		},
		[]string{"namespace", "name", "outcome"},
	)

	reconcileDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "bigbang_operator_reconcile_duration_seconds",
			Help:    "Wall-clock time spent in Reconcile, including apply + prune + status update.",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 12), // 10ms .. ~40s
		},
		[]string{"namespace", "name"},
	)

	appliedResources = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "bigbang_operator_applied_resources",
			Help: "Number of child objects emitted on the last successful reconcile.",
		},
		[]string{"namespace", "name"},
	)
)

func init() {
	metrics.Registry.MustRegister(reconcileTotal, reconcileDurationSeconds, appliedResources)
}

// Outcome labels for reconcileTotal. Keep this set tight — every new value
// is a new time series.
const (
	outcomeSuccess          = "success"
	outcomeGenerationFailed = "generation_failed"
	outcomeApplyFailed      = "apply_failed"
	outcomePruneFailed      = "prune_failed"
)
