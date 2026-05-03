// Package metrics defines the daemon's Recorder interface and a NoOp
// implementation. Concrete Prometheus-backed implementations live in
// internal/runtime/metrics/. Per core-belief #3, no service / repo / runtime
// non-metrics package may import prometheus/client_golang directly; they
// take a Recorder by interface.
package metrics

import "time"

// Recorder is the daemon-internal observability sink.
//
// All methods are best-effort and must never panic. The NoOp recorder is
// the zero value; tests use it.
type Recorder interface {
	// CounterAdd increments a labeled counter.
	CounterAdd(name string, labels map[string]string, delta float64)
	// HistogramObserve records a Default-bucket histogram observation.
	HistogramObserve(name string, labels map[string]string, value float64)
	// GaugeSet sets a labeled gauge.
	GaugeSet(name string, labels map[string]string, value float64)
}

// NoOp returns a Recorder that drops every emission. Default for tests.
func NoOp() Recorder { return noOp{} }

type noOp struct{}

func (noOp) CounterAdd(string, map[string]string, float64)       {}
func (noOp) HistogramObserve(string, map[string]string, float64) {}
func (noOp) GaugeSet(string, map[string]string, float64)         {}

// Names of metrics emitted by the daemon. Centralized so the metric
// catalogue stays visible in one place. Matches docs/conventions/metrics.md
// (livepeer_videoworker_* prefix).
const (
	NameBuildInfo            = "livepeer_videoworker_build_info"
	NameJobsTotal            = "livepeer_videoworker_jobs_total"
	NameJobPhaseDuration     = "livepeer_videoworker_job_phase_duration_seconds"
	NameJobActive            = "livepeer_videoworker_jobs_active"
	NameWorkUnitsTotal       = "livepeer_videoworker_work_units_total"
	NameStreamsTotal         = "livepeer_videoworker_streams_total"
	NameStreamsActive        = "livepeer_videoworker_streams_active"
	NameStreamDuration       = "livepeer_videoworker_stream_duration_seconds"
	NameDebitTotal           = "livepeer_videoworker_payment_debits_total"
	NameWebhookTotal         = "livepeer_videoworker_webhook_deliveries_total"
	NameRegistryRefreshTotal = "livepeer_videoworker_registry_refresh_total"
	NameGPUDetected          = "livepeer_videoworker_gpu_detected"
	NameGPUMaxSessions       = "livepeer_videoworker_gpu_max_sessions"
	NameGPUSlotsTotal        = "livepeer_videoworker_gpu_slots_total"
	NameGPUSessionsInflight  = "livepeer_videoworker_gpu_sessions_inflight"
	NameGPULiveReserved      = "livepeer_videoworker_gpu_live_reserved_slots"
	NameGPUCostCapacity      = "livepeer_videoworker_gpu_cost_capacity"
	NameGPUCostInflight      = "livepeer_videoworker_gpu_cost_inflight"
	NameGPULiveReservedCost  = "livepeer_videoworker_gpu_live_reserved_cost"
	NameGPUBatchQueued       = "livepeer_videoworker_gpu_batch_queue_depth"
)

// Timer is a small helper that records elapsed time on Stop().
type Timer struct {
	r       Recorder
	name    string
	labels  map[string]string
	started time.Time
}

// StartTimer returns a Timer that records on Stop(). Always returns a
// usable timer, even with a NoOp recorder.
func StartTimer(r Recorder, name string, labels map[string]string) *Timer {
	if r == nil {
		r = NoOp()
	}
	return &Timer{r: r, name: name, labels: labels, started: time.Now()}
}

// Stop records the elapsed time as a histogram observation in seconds.
func (t *Timer) Stop() {
	if t == nil {
		return
	}
	t.r.HistogramObserve(t.name, t.labels, time.Since(t.started).Seconds())
}
