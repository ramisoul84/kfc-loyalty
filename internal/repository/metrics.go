package repository

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/ramisoul84/kfc-loyalty/pkg/metrics"
)

// pointsTotal counts points moved through the ledger by entry type
// (welcome, earn, redeem, refund, adjust), as absolute values.
var pointsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "loyalty_points_total",
	Help: "Points written to the ledger, by entry type (absolute values).",
}, []string{"type"})

func init() {
	metrics.Registry.MustRegister(pointsTotal)
}
