package monitor

import (
	"testing"
	"time"

	"github.com/wyw14/cry-96/internal/model"
)

func newTestTelemetry(now *time.Time) *Telemetry {
	return NewTelemetry(10*time.Second, 30*time.Second, 30*time.Second, func() time.Time { return *now })
}

func heartbeat(chamberID string, device model.DeviceKind, status model.DeviceStatus, observedAt time.Time) model.DeviceHeartbeat {
	return model.DeviceHeartbeat{ChamberID: chamberID, Device: device, Status: status, ObservedAt: observedAt}
}

func TestTrackedDeviceStartsUnknown(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	telemetry := newTestTelemetry(&now)
	telemetry.Track("chamber-1", model.DeviceUpstreamGate)

	view := telemetry.Health("chamber-1", model.DeviceUpstreamGate)
	if view.State != HealthUnknown {
		t.Fatalf("expected unknown state, got %s", view.State)
	}
	if view.Online {
		t.Fatal("unknown device must not report online")
	}
	if telemetry.AllOnline("chamber-1") {
		t.Fatal("chamber with unknown device must not report all online")
	}
}

func TestFreshHeartbeatMarksDeviceNormal(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	telemetry := newTestTelemetry(&now)
	telemetry.Track("chamber-1", model.DeviceFillValve)

	verdict, view, err := telemetry.Ingest(heartbeat("chamber-1", model.DeviceFillValve, model.DeviceStatusRunning, now.Add(-2*time.Second)))
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if verdict != VerdictFresh {
		t.Fatalf("expected fresh verdict, got %s", verdict)
	}
	if view.State != HealthNormal || !view.Online {
		t.Fatalf("expected normal online device, got %s", view.State)
	}
	if !telemetry.AllOnline("chamber-1") {
		t.Fatal("chamber should report all online after fresh heartbeat")
	}
}

func TestLateHeartbeatIsAppliedAndCounted(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	telemetry := newTestTelemetry(&now)
	telemetry.Track("chamber-1", model.DeviceDrainValve)

	verdict, view, err := telemetry.Ingest(heartbeat("chamber-1", model.DeviceDrainValve, model.DeviceStatusRunning, now.Add(-25*time.Second)))
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if verdict != VerdictLate {
		t.Fatalf("expected late verdict, got %s", verdict)
	}
	if view.State != HealthNormal {
		t.Fatalf("late heartbeat still applies, got %s", view.State)
	}
	if view.LateCount != 1 {
		t.Fatalf("expected late count 1, got %d", view.LateCount)
	}
	if view.Message != "telemetry delayed" {
		t.Fatalf("expected delayed message, got %q", view.Message)
	}
}

func TestOutOfOrderHeartbeatDoesNotOverwrite(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	telemetry := newTestTelemetry(&now)
	telemetry.Track("chamber-1", model.DeviceUpstreamGate)

	newer := now.Add(-5 * time.Second)
	if _, _, err := telemetry.Ingest(heartbeat("chamber-1", model.DeviceUpstreamGate, model.DeviceStatusRunning, newer)); err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	// Stale fault report with an older observation time must be rejected.
	verdict, view, err := telemetry.Ingest(heartbeat("chamber-1", model.DeviceUpstreamGate, model.DeviceStatusFault, newer.Add(-10*time.Second)))
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if verdict != VerdictOutOfOrder {
		t.Fatalf("expected out_of_order verdict, got %s", verdict)
	}
	if view.State != HealthNormal {
		t.Fatalf("stale heartbeat must not change health, got %s", view.State)
	}
	if view.Status != model.DeviceStatusRunning {
		t.Fatalf("stale heartbeat must not overwrite status, got %s", view.Status)
	}
	if view.ObservedAt == nil || !view.ObservedAt.Equal(newer) {
		t.Fatalf("stale heartbeat must not overwrite observation time, got %v", view.ObservedAt)
	}
	if view.OutOfOrderCount != 1 {
		t.Fatalf("expected out-of-order count 1, got %d", view.OutOfOrderCount)
	}
	// Same observation time is not newer and is rejected as well.
	verdict, _, err = telemetry.Ingest(heartbeat("chamber-1", model.DeviceUpstreamGate, model.DeviceStatusFault, newer))
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if verdict != VerdictOutOfOrder {
		t.Fatalf("equal observation time must be out_of_order, got %s", verdict)
	}
}

func TestHeartbeatTimeoutMarksDeviceOffline(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	telemetry := newTestTelemetry(&now)
	telemetry.Track("chamber-1", model.DeviceDownstreamGate)

	if _, _, err := telemetry.Ingest(heartbeat("chamber-1", model.DeviceDownstreamGate, model.DeviceStatusRunning, now)); err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	now = now.Add(31 * time.Second)
	view := telemetry.Health("chamber-1", model.DeviceDownstreamGate)
	if view.State != HealthOffline {
		t.Fatalf("expected offline after timeout, got %s", view.State)
	}
	if view.Online {
		t.Fatal("timed-out device must not report online")
	}
	if view.Message != "heartbeat timeout" {
		t.Fatalf("expected timeout message, got %q", view.Message)
	}
}

func TestFaultStatusMarksDeviceOffline(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	telemetry := newTestTelemetry(&now)
	telemetry.Track("chamber-1", model.DeviceFillValve)

	_, view, err := telemetry.Ingest(heartbeat("chamber-1", model.DeviceFillValve, model.DeviceStatusFault, now))
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if view.State != HealthOffline {
		t.Fatalf("expected offline for fault status, got %s", view.State)
	}
}

func TestDeviceRecoversAfterTimeout(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	telemetry := newTestTelemetry(&now)
	telemetry.Track("chamber-1", model.DeviceDrainValve)

	if _, _, err := telemetry.Ingest(heartbeat("chamber-1", model.DeviceDrainValve, model.DeviceStatusRunning, now)); err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	now = now.Add(time.Minute)
	if view := telemetry.Health("chamber-1", model.DeviceDrainValve); view.State != HealthOffline {
		t.Fatalf("expected offline after timeout, got %s", view.State)
	}
	verdict, view, err := telemetry.Ingest(heartbeat("chamber-1", model.DeviceDrainValve, model.DeviceStatusRunning, now))
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if verdict != VerdictFresh || view.State != HealthNormal {
		t.Fatalf("expected recovery to normal, got %s/%s", verdict, view.State)
	}
}

func TestIngestRejectsInvalidInput(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	telemetry := newTestTelemetry(&now)
	telemetry.Track("chamber-1", model.DeviceUpstreamGate)

	if _, _, err := telemetry.Ingest(heartbeat("chamber-1", model.DeviceUpstreamGate, model.DeviceStatusRunning, time.Time{})); err == nil {
		t.Fatal("expected error for missing observation time")
	}
	if _, _, err := telemetry.Ingest(heartbeat("chamber-1", model.DeviceUpstreamGate, model.DeviceStatusRunning, now.Add(time.Minute))); err == nil {
		t.Fatal("expected error for observation too far in the future")
	}
	if _, _, err := telemetry.Ingest(heartbeat("chamber-9", model.DeviceUpstreamGate, model.DeviceStatusRunning, now)); err == nil {
		t.Fatal("expected error for untracked device")
	}
	if _, _, err := telemetry.Ingest(heartbeat("chamber-1", model.DeviceUpstreamGate, "spinning", now)); err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestListCoversEveryTrackedDevice(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	telemetry := newTestTelemetry(&now)
	devices := []model.DeviceKind{model.DeviceUpstreamGate, model.DeviceDownstreamGate, model.DeviceFillValve, model.DeviceDrainValve}
	for _, chamberID := range []string{"chamber-1", "chamber-2"} {
		for _, device := range devices {
			telemetry.Track(chamberID, device)
		}
	}
	views := telemetry.List()
	if len(views) != 8 {
		t.Fatalf("expected 8 tracked devices, got %d", len(views))
	}
	for _, view := range views {
		if view.State != HealthUnknown {
			t.Fatalf("expected all devices unknown at startup, got %s for %s/%s", view.State, view.ChamberID, view.Device)
		}
	}
}
