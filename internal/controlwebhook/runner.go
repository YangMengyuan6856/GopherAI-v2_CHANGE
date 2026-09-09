package controlwebhook

import (
	"context"
	"log"
	"time"

	"GopherAI/internal/observability"
)

type SnapshotReader interface {
	LatestProductionAnalysis(context.Context) (observability.ProductionAnomalySnapshot, error)
}

// RunReconciler converts durable metric windows into durable incident and
// webhook events. It runs independently from HTTP request handling.
func RunReconciler(ctx context.Context, reader SnapshotReader, reconciler *Reconciler, initialDelay time.Duration, interval time.Duration, logger *log.Logger) {
	if reader == nil || reconciler == nil || interval <= 0 {
		return
	}
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		snapshot, err := reader.LatestProductionAnalysis(runCtx)
		result := ReconcileResult{}
		if err == nil {
			result, err = reconciler.Reconcile(runCtx, snapshot)
		}
		cancel()
		if logger != nil {
			if err != nil {
				logger.Print(`{"event":"control_incident_reconcile","status":"error","reason_code":"CONTROL_RECONCILE_FAILED"}`)
			} else {
				logger.Printf(`{"event":"control_incident_reconcile","status":"success","examined":%d,"queued":%d}`, result.Examined, result.Queued)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
