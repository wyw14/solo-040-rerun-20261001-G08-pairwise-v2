package monitor

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/wyw14/cry-96/internal/model"
)

// HealthState is the effective condition of a tracked device.
type HealthState string

const (
	HealthUnknown HealthState = "unknown"
	HealthNormal  HealthState = "normal"
	HealthOffline HealthState = "offline"
)

// Verdict classifies an ingested heartbeat.
type Verdict string

const (
	VerdictFresh      Verdict = "fresh"
	VerdictLate       Verdict = "late"
	VerdictOutOfOrder Verdict = "out_of_order"
)

// DeviceHealth is the read-side view of one tracked device.
type DeviceHealth struct {
	ChamberID       string             `json:"chamber_id"`
	Device          model.DeviceKind   `json:"device"`
	State           HealthState        `json:"state"`
	Online          bool               `json:"online"`
	Message         string             `json:"message"`
	Status          model.DeviceStatus `json:"status,omitempty"`
	ObservedAt      *time.Time         `json:"observed_at,omitempty"`
	ReceivedAt      *time.Time         `json:"received_at,omitempty"`
	CheckedAt       time.Time          `json:"checked_at"`
	LateCount       int                `json:"late_count"`
	OutOfOrderCount int                `json:"out_of_order_count"`
}

// observation is the stored latest applied heartbeat plus stream anomalies.
type observation struct {
	status          model.DeviceStatus
	observedAt      time.Time
	receivedAt      time.Time
	lastLate        bool
	lateCount       int
	outOfOrderCount int
}

// Telemetry collects gate and valve heartbeats and derives device health.
// Only heartbeats newer than the applied observation are accepted, so stale
// telemetry never overwrites fresher data.
type Telemetry struct {
	mu           sync.RWMutex
	observations map[string]*observation
	lateAfter    time.Duration
	timeout      time.Duration
	maxFuture    time.Duration
	now          func() time.Time
}

// NewTelemetry builds a collector. lateAfter flags heartbeats whose
// observation lags arrival; timeout marks silent devices offline; maxFuture
// rejects observations stamped too far ahead of the server clock.
func NewTelemetry(lateAfter, timeout, maxFuture time.Duration, now func() time.Time) *Telemetry {
	if now == nil {
		now = time.Now
	}
	return &Telemetry{
		observations: make(map[string]*observation),
		lateAfter:    lateAfter, timeout: timeout, maxFuture: maxFuture, now: now,
	}
}

func telemetryKey(chamberID string, device model.DeviceKind) string {
	return chamberID + ":" + string(device)
}

// Track registers a known device so it is reported as unknown until the
// first heartbeat arrives.
func (t *Telemetry) Track(chamberID string, device model.DeviceKind) {
	t.mu.Lock()
	key := telemetryKey(chamberID, device)
	if _, ok := t.observations[key]; !ok {
		t.observations[key] = &observation{}
	}
	t.mu.Unlock()
}

// Ingest validates and classifies a heartbeat, applying it only when its
// observation is newer than the one already stored.
func (t *Telemetry) Ingest(heartbeat model.DeviceHeartbeat) (Verdict, DeviceHealth, error) {
	if err := heartbeat.Validate(); err != nil {
		return "", DeviceHealth{}, err
	}
	receivedAt := t.now().UTC()
	if heartbeat.ObservedAt.After(receivedAt.Add(t.maxFuture)) {
		return "", DeviceHealth{}, fmt.Errorf("%w: observation %s is too far in the future", model.ErrInvalidHeartbeat, heartbeat.ObservedAt)
	}
	key := telemetryKey(heartbeat.ChamberID, heartbeat.Device)
	t.mu.Lock()
	record, ok := t.observations[key]
	if !ok {
		t.mu.Unlock()
		return "", DeviceHealth{}, fmt.Errorf("device %s is not tracked in chamber %s", heartbeat.Device, heartbeat.ChamberID)
	}
	observedAt := heartbeat.ObservedAt.UTC()
	if !record.receivedAt.IsZero() && !observedAt.After(record.observedAt) {
		record.outOfOrderCount++
		t.mu.Unlock()
		return VerdictOutOfOrder, t.Health(heartbeat.ChamberID, heartbeat.Device), nil
	}
	verdict := VerdictFresh
	late := receivedAt.Sub(observedAt) > t.lateAfter
	if late {
		verdict = VerdictLate
		record.lateCount++
	}
	record.status = heartbeat.Status
	record.observedAt = observedAt
	record.receivedAt = receivedAt
	record.lastLate = late
	t.mu.Unlock()
	return verdict, t.Health(heartbeat.ChamberID, heartbeat.Device), nil
}

// Health derives the current view of one tracked device.
func (t *Telemetry) Health(chamberID string, device model.DeviceKind) DeviceHealth {
	t.mu.RLock()
	record, ok := t.observations[telemetryKey(chamberID, device)]
	var copied observation
	if ok {
		copied = *record
	}
	t.mu.RUnlock()
	if !ok {
		return DeviceHealth{
			ChamberID: chamberID, Device: device,
			State: HealthUnknown, Message: "device is not tracked", CheckedAt: t.now().UTC(),
		}
	}
	return t.derive(chamberID, device, copied)
}

// List reports the current health of every tracked device.
func (t *Telemetry) List() []DeviceHealth {
	t.mu.RLock()
	type entry struct {
		chamberID string
		device    model.DeviceKind
		record    observation
	}
	entries := make([]entry, 0, len(t.observations))
	for key, record := range t.observations {
		chamberID, device := splitTelemetryKey(key)
		entries = append(entries, entry{chamberID: chamberID, device: device, record: *record})
	}
	t.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].chamberID == entries[j].chamberID {
			return entries[i].device < entries[j].device
		}
		return entries[i].chamberID < entries[j].chamberID
	})
	result := make([]DeviceHealth, 0, len(entries))
	for _, item := range entries {
		result = append(result, t.derive(item.chamberID, item.device, item.record))
	}
	return result
}

// derive computes the effective health of one observation at the current time.
func (t *Telemetry) derive(chamberID string, device model.DeviceKind, record observation) DeviceHealth {
	view := DeviceHealth{
		ChamberID: chamberID, Device: device,
		State: HealthUnknown, Message: "no telemetry received", CheckedAt: t.now().UTC(),
		Status: record.status,
		LateCount: record.lateCount, OutOfOrderCount: record.outOfOrderCount,
	}
	if record.receivedAt.IsZero() {
		return view
	}
	observedAt := record.observedAt
	receivedAt := record.receivedAt
	view.ObservedAt = &observedAt
	view.ReceivedAt = &receivedAt
	if t.now().UTC().Sub(record.receivedAt) > t.timeout {
		view.State = HealthOffline
		view.Message = "heartbeat timeout"
		return view
	}
	if record.status != model.DeviceStatusRunning {
		view.State = HealthOffline
		view.Message = fmt.Sprintf("device reports %s", record.status)
		return view
	}
	view.State = HealthNormal
	view.Online = true
	view.Message = "telemetry current"
	if record.lastLate {
		view.Message = "telemetry delayed"
	}
	return view
}

func splitTelemetryKey(key string) (string, model.DeviceKind) {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == ':' {
			return key[:i], model.DeviceKind(key[i+1:])
		}
	}
	return key, ""
}

// AllOnline reports whether every tracked device in the chamber is normal.
func (t *Telemetry) AllOnline(chamberID string) bool {
	for _, view := range t.List() {
		if view.ChamberID == chamberID && view.State != HealthNormal {
			return false
		}
	}
	return true
}
