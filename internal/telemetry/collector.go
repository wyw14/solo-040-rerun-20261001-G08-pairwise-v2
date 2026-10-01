// Package telemetry ingests gate and valve heartbeats. It distinguishes
// current reports from late, out-of-order and timed-out deliveries so that
// stale traffic can never overwrite a newer device observation.
package telemetry

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/wyw14/cry-96/internal/model"
	"github.com/wyw14/cry-96/internal/monitor"
)

type RejectReason string

const (
	ReasonInvalid    RejectReason = "invalid"
	ReasonFuture     RejectReason = "future_timestamp"
	ReasonDuplicate  RejectReason = "duplicate"
	ReasonLate       RejectReason = "late"
	ReasonOutOfOrder RejectReason = "out_of_order"
)

// DefaultTimeout is how long after the newest observation a device may stay
// online before a missed heartbeat marks it offline.
const DefaultTimeout = 30 * time.Second

// clockSkew tolerates devices whose clock runs slightly ahead of the server.
const clockSkew = 2 * time.Second

// IngestResult reports what happened to one heartbeat without exposing locks.
type IngestResult struct {
	Accepted    bool                     `json:"accepted"`
	Reason      RejectReason             `json:"reason,omitempty"`
	Observation *model.DeviceObservation `json:"observation,omitempty"`
	Health      monitor.DeviceHealth     `json:"health"`
}

type deviceRecord struct {
	observation model.DeviceObservation
	hasSequence bool
}

// Collector accepts heartbeats, keeps the newest observation per device and
// drives the shared health registry.
type Collector struct {
	mu      sync.RWMutex
	health  *monitor.HealthRegistry
	records map[string]deviceRecord
	timeout time.Duration
	skew    time.Duration
	now     func() time.Time
}

func New(health *monitor.HealthRegistry, timeout time.Duration, now func() time.Time) *Collector {
	if now == nil {
		now = time.Now
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Collector{
		health: health, records: make(map[string]deviceRecord),
		timeout: timeout, skew: clockSkew, now: now,
	}
}

func key(chamberID string, device model.DeviceKind) string {
	return chamberID + ":" + string(device)
}

// Ingest validates and applies one heartbeat. Rejected heartbeats leave both
// the latest observation and health untouched.
func (c *Collector) Ingest(heartbeat model.DeviceHeartbeat) IngestResult {
	if err := heartbeat.Validate(); err != nil {
		return IngestResult{Accepted: false, Reason: ReasonInvalid, Health: c.healthSnapshot(heartbeat.ChamberID, heartbeat.Device)}
	}
	observedAt := heartbeat.ObservedAt.UTC()
	receivedAt := c.now().UTC()
	if observedAt.After(receivedAt.Add(c.skew)) {
		return IngestResult{Accepted: false, Reason: ReasonFuture, Health: c.healthSnapshot(heartbeat.ChamberID, heartbeat.Device)}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	mapKey := key(heartbeat.ChamberID, heartbeat.Device)
	current, exists := c.records[mapKey]

	// When both reports carry a sequence number it is the authoritative
	// ordering channel; otherwise fall back to the device timestamp.
	sequenceKnown := heartbeat.Sequence > 0 && exists && current.hasSequence
	switch {
	case sequenceKnown && heartbeat.Sequence == current.observation.Sequence:
		return IngestResult{Accepted: false, Reason: ReasonDuplicate, Health: c.healthSnapshotLocked(heartbeat.ChamberID, heartbeat.Device)}
	case sequenceKnown && heartbeat.Sequence < current.observation.Sequence:
		return IngestResult{Accepted: false, Reason: ReasonOutOfOrder, Health: c.healthSnapshotLocked(heartbeat.ChamberID, heartbeat.Device)}
	case !sequenceKnown && exists && observedAt.Before(current.observation.ObservedAt):
		return IngestResult{Accepted: false, Reason: ReasonLate, Health: c.healthSnapshotLocked(heartbeat.ChamberID, heartbeat.Device)}
	}

	observation := model.DeviceObservation{
		ChamberID: heartbeat.ChamberID, Device: heartbeat.Device,
		Status: heartbeat.Status, ObservedAt: observedAt,
		ReceivedAt: receivedAt, Sequence: heartbeat.Sequence,
	}
	c.records[mapKey] = deviceRecord{observation: observation, hasSequence: heartbeat.Sequence > 0}
	health := c.health.ReportOnline(heartbeat.ChamberID, heartbeat.Device, string(heartbeat.Status), observedAt, receivedAt)
	return IngestResult{Accepted: true, Observation: &observation, Health: health}
}

// IngestBatch applies heartbeats independently and returns one result each.
func (c *Collector) IngestBatch(heartbeats []model.DeviceHeartbeat) []IngestResult {
	results := make([]IngestResult, len(heartbeats))
	for i := range heartbeats {
		results[i] = c.Ingest(heartbeats[i])
	}
	return results
}

// Observation returns the newest accepted observation for a device.
func (c *Collector) Observation(chamberID string, device model.DeviceKind) (model.DeviceObservation, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	record, ok := c.records[key(chamberID, device)]
	return record.observation, ok
}

// DeviceView joins connectivity health with the latest operational status.
type DeviceView struct {
	monitor.DeviceHealth
	Observation *model.DeviceObservation `json:"observation,omitempty"`
}

func (c *Collector) view(chamberID string, device model.DeviceKind) DeviceView {
	health, _ := c.health.Get(chamberID, device)
	view := DeviceView{DeviceHealth: health}
	if record, ok := c.records[key(chamberID, device)]; ok {
		observation := record.observation
		view.Observation = &observation
	}
	return view
}

// Sweep marks online devices offline when their newest observation is older
// than the timeout. Unknown devices and already-offline devices are left
// alone, and no observation data is altered.
func (c *Collector) Sweep(now time.Time) []DeviceView {
	c.mu.Lock()
	defer c.mu.Unlock()
	stamp := now.UTC()
	var changed []DeviceView
	for _, record := range c.records {
		observation := record.observation
		health, ok := c.health.Get(observation.ChamberID, observation.Device)
		if !ok || health.State != monitor.HealthOnline {
			continue
		}
		if stamp.Sub(observation.ObservedAt) <= c.timeout {
			continue
		}
		updated := c.health.ReportOffline(observation.ChamberID, observation.Device, "heartbeat overdue", stamp)
		changed = append(changed, DeviceView{
			DeviceHealth: updated,
			Observation:  &observation,
		})
	}
	sort.Slice(changed, func(i, j int) bool {
		if changed[i].ChamberID == changed[j].ChamberID {
			return changed[i].Device < changed[j].Device
		}
		return changed[i].ChamberID < changed[j].ChamberID
	})
	return changed
}

// Views returns health and latest observation for every registered device.
func (c *Collector) Views() []DeviceView {
	c.mu.RLock()
	defer c.mu.RUnlock()
	views := make([]DeviceView, 0)
	seen := make(map[string]bool, len(c.records))
	for _, record := range c.records {
		mapKey := key(record.observation.ChamberID, record.observation.Device)
		if seen[mapKey] {
			continue
		}
		seen[mapKey] = true
		views = append(views, c.view(record.observation.ChamberID, record.observation.Device))
	}
	for _, health := range c.health.List() {
		mapKey := key(health.ChamberID, health.Device)
		if seen[mapKey] {
			continue
		}
		seen[mapKey] = true
		views = append(views, DeviceView{DeviceHealth: health})
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].ChamberID == views[j].ChamberID {
			return views[i].Device < views[j].Device
		}
		return views[i].ChamberID < views[j].ChamberID
	})
	return views
}

// ChamberViews returns health and latest observation for one chamber.
func (c *Collector) ChamberViews(chamberID string) []DeviceView {
	all := c.Views()
	views := make([]DeviceView, 0, len(all))
	for _, view := range all {
		if view.ChamberID == chamberID {
			views = append(views, view)
		}
	}
	return views
}

// Run periodically flips timed-out devices offline until the context ends.
func (c *Collector) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = c.timeout / 3
		if interval <= 0 {
			interval = 10 * time.Second
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Sweep(c.now())
		}
	}
}

var _ = context.Canceled

func (c *Collector) healthSnapshot(chamberID string, device model.DeviceKind) monitor.DeviceHealth {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.healthSnapshotLocked(chamberID, device)
}

func (c *Collector) healthSnapshotLocked(chamberID string, device model.DeviceKind) monitor.DeviceHealth {
	health, ok := c.health.Get(chamberID, device)
	if !ok {
		return monitor.DeviceHealth{ChamberID: chamberID, Device: device, State: monitor.HealthUnknown, CheckedAt: c.now().UTC()}
	}
	return health
}
