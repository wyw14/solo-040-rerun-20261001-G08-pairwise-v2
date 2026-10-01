package telemetry

import (
	"testing"
	"time"

	"github.com/wyw14/cry-96/internal/model"
	"github.com/wyw14/cry-96/internal/monitor"
)

func newTestCollector(now time.Time) (*Collector, *monitor.HealthRegistry, *time.Time) {
	health := monitor.NewHealthRegistry()
	clock := now
	collector := New(health, 30*time.Second, func() time.Time { return clock })
	health.Seed("chamber-1", model.DeviceUpstreamGate, clock)
	return collector, health, &clock
}

func beat(device model.DeviceKind, status model.DeviceStatus, at time.Time, sequence uint64) model.DeviceHeartbeat {
	return model.DeviceHeartbeat{
		ChamberID: "chamber-1", Device: device, Status: status,
		ObservedAt: at, Sequence: sequence,
	}
}

func TestStartsUnknown(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	collector, health, _ := newTestCollector(now)

	record, ok := health.Get("chamber-1", model.DeviceUpstreamGate)
	if !ok || record.State != monitor.HealthUnknown {
		t.Fatalf("expected unknown seed state, got %+v (ok=%v)", record, ok)
	}
	if health.AllOnline("chamber-1") {
		t.Fatal("chamber with only unknown devices must not report all online")
	}
	if views := collector.Views(); len(views) != 1 || views[0].State != monitor.HealthUnknown {
		t.Fatalf("expected one unknown view, got %+v", views)
	}
}

func TestFirstHeartbeatMovesOnline(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	collector, health, clock := newTestCollector(now)

	result := collector.Ingest(beat(model.DeviceUpstreamGate, model.StatusRunning, now, 1))
	if !result.Accepted {
		t.Fatalf("heartbeat rejected: %s", result.Reason)
	}
	record, _ := health.Get("chamber-1", model.DeviceUpstreamGate)
	if record.State != monitor.HealthOnline || !record.Online {
		t.Fatalf("expected online, got %+v", record)
	}
	if !health.AllOnline("chamber-1") {
		t.Fatal("online device should make chamber all-online")
	}

	// Observation is the latest accepted report.
	observation, ok := collector.Observation("chamber-1", model.DeviceUpstreamGate)
	if !ok || observation.Status != model.StatusRunning || observation.ReceivedAt != *clock {
		t.Fatalf("unexpected observation: %+v", observation)
	}
}

func TestLateTimestampDoesNotOverwrite(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	collector, _, clock := newTestCollector(now)

	*clock = now.Add(20 * time.Second)
	collector.Ingest(beat(model.DeviceUpstreamGate, model.StatusRunning, now.Add(20*time.Second), 0))
	*clock = now.Add(25 * time.Second)
	result := collector.Ingest(beat(model.DeviceUpstreamGate, model.StatusFault, now.Add(10*time.Second), 0))
	if result.Accepted || result.Reason != ReasonLate {
		t.Fatalf("expected late rejection, got accepted=%v reason=%s", result.Accepted, result.Reason)
	}
	observation, _ := collector.Observation("chamber-1", model.DeviceUpstreamGate)
	if observation.Status != model.StatusRunning || !observation.ObservedAt.Equal(now.Add(20*time.Second)) {
		t.Fatalf("stale heartbeat overwrote newer data: %+v", observation)
	}
}

func TestOutOfOrderSequenceDoesNotOverwrite(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	collector, _, clock := newTestCollector(now)

	collector.Ingest(beat(model.DeviceUpstreamGate, model.StatusRunning, now, 5))
	*clock = now.Add(time.Second)
	older := collector.Ingest(beat(model.DeviceUpstreamGate, model.StatusFault, now.Add(time.Second), 4))
	if older.Accepted || older.Reason != ReasonOutOfOrder {
		t.Fatalf("expected out_of_order, got accepted=%v reason=%s", older.Accepted, older.Reason)
	}
	*clock = now.Add(2 * time.Second)
	duplicate := collector.Ingest(beat(model.DeviceUpstreamGate, model.StatusFault, now.Add(2*time.Second), 5))
	if duplicate.Accepted || duplicate.Reason != ReasonDuplicate {
		t.Fatalf("expected duplicate, got accepted=%v reason=%s", duplicate.Accepted, duplicate.Reason)
	}
	// Even with a later wall-clock timestamp, a lower sequence must lose.
	*clock = now.Add(3 * time.Second)
	newerSeq := collector.Ingest(beat(model.DeviceUpstreamGate, model.StatusStopped, now.Add(3*time.Second), 6))
	if !newerSeq.Accepted {
		t.Fatalf("newer sequence should be accepted, reason=%s", newerSeq.Reason)
	}
	observation, _ := collector.Observation("chamber-1", model.DeviceUpstreamGate)
	if observation.Status != model.StatusStopped || observation.Sequence != 6 {
		t.Fatalf("unexpected observation: %+v", observation)
	}
}

func TestSweepMarksTimeoutOfflineAndKeepsObservation(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	collector, health, clock := newTestCollector(now)

	collector.Ingest(beat(model.DeviceUpstreamGate, model.StatusRunning, now, 1))
	*clock = now.Add(31 * time.Second)
	changed := collector.Sweep(*clock)
	if len(changed) != 1 || changed[0].State != monitor.HealthOffline {
		t.Fatalf("expected one offline transition, got %+v", changed)
	}
	record, _ := health.Get("chamber-1", model.DeviceUpstreamGate)
	if record.State != monitor.HealthOffline {
		t.Fatalf("expected offline, got %+v", record)
	}
	// The last good observation remains visible and is not rewritten.
	observation, ok := collector.Observation("chamber-1", model.DeviceUpstreamGate)
	if !ok || observation.Status != model.StatusRunning {
		t.Fatalf("last observation must survive timeout: %+v", observation)
	}

	// Sweeping again is a no-op while offline.
	if again := collector.Sweep(clock.Add(time.Minute)); len(again) != 0 {
		t.Fatalf("offline device should not transition again: %+v", again)
	}

	// A fresh heartbeat restores online state.
	result := collector.Ingest(beat(model.DeviceUpstreamGate, model.StatusRunning, clock.Add(time.Second), 2))
	if !result.Accepted || result.Health.State != monitor.HealthOnline {
		t.Fatalf("expected recovery to online, got %+v", result)
	}
}

func TestFutureTimestampRejected(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	collector, _, _ := newTestCollector(now)

	result := collector.Ingest(beat(model.DeviceUpstreamGate, model.StatusRunning, now.Add(time.Minute), 1))
	if result.Accepted || result.Reason != ReasonFuture {
		t.Fatalf("expected future_timestamp, got accepted=%v reason=%s", result.Accepted, result.Reason)
	}
}

func TestInvalidHeartbeatRejected(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	collector, _, _ := newTestCollector(now)

	invalid := model.DeviceHeartbeat{ChamberID: "chamber-1", Device: "pump", Status: model.StatusRunning, ObservedAt: now}
	if result := collector.Ingest(invalid); result.Accepted || result.Reason != ReasonInvalid {
		t.Fatalf("expected invalid, got %+v", result)
	}
}

func TestBatchIndependent(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	collector, _, _ := newTestCollector(now)

	results := collector.IngestBatch([]model.DeviceHeartbeat{
		beat(model.DeviceUpstreamGate, model.StatusRunning, now, 1),
		beat(model.DeviceUpstreamGate, model.StatusFault, now, 1),
		{ChamberID: "chamber-1", Device: model.DeviceFillValve, Status: model.StatusStopped, ObservedAt: now},
	})
	if !results[0].Accepted || results[1].Accepted || !results[2].Accepted {
		t.Fatalf("expected accepted/rejected/accepted, got %+v", results)
	}
	if results[1].Reason != ReasonDuplicate {
		t.Fatalf("expected duplicate reason, got %s", results[1].Reason)
	}
}
