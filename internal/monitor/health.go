package monitor

import (
	"sort"
	"sync"
	"time"

	"github.com/wyw14/cry-96/internal/model"
)

type HealthState string

const (
	HealthUnknown HealthState = "unknown"
	HealthOnline  HealthState = "online"
	HealthOffline HealthState = "offline"
)

type DeviceHealth struct {
	ChamberID      string           `json:"chamber_id"`
	Device         model.DeviceKind `json:"device"`
	State          HealthState      `json:"state"`
	Online         bool             `json:"online"`
	Message        string           `json:"message,omitempty"`
	LastObservedAt time.Time        `json:"last_observed_at,omitempty"`
	LastReceivedAt time.Time        `json:"last_received_at,omitempty"`
	ChangedAt      time.Time        `json:"changed_at,omitempty"`
	CheckedAt      time.Time        `json:"checked_at"`
}

// HealthRegistry provides current connectivity health, not historical reports.
// Devices start unknown and only become online after an accepted heartbeat;
// missed heartbeats move them to offline.
type HealthRegistry struct {
	mu      sync.RWMutex
	records map[string]DeviceHealth
}

func NewHealthRegistry() *HealthRegistry {
	return &HealthRegistry{records: make(map[string]DeviceHealth)}
}

func healthKey(chamberID string, device model.DeviceKind) string {
	return chamberID + ":" + string(device)
}

// Seed registers expected equipment as unknown until the first heartbeat.
func (r *HealthRegistry) Seed(chamberID string, device model.DeviceKind, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := healthKey(chamberID, device)
	if _, exists := r.records[key]; exists {
		return
	}
	r.records[key] = DeviceHealth{
		ChamberID: chamberID, Device: device, State: HealthUnknown,
		CheckedAt: now.UTC(),
	}
}

func (r *HealthRegistry) report(chamberID string, device model.DeviceKind, state HealthState, message string, observedAt, receivedAt time.Time) DeviceHealth {
	now := receivedAt.UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	key := healthKey(chamberID, device)
	current := r.records[key]
	record := DeviceHealth{
		ChamberID: chamberID, Device: device, State: state,
		Online: state == HealthOnline, Message: message,
		LastObservedAt: current.LastObservedAt, LastReceivedAt: current.LastReceivedAt,
		ChangedAt: current.ChangedAt, CheckedAt: now,
	}
	if !observedAt.IsZero() {
		record.LastObservedAt = observedAt.UTC()
	}
	if state == HealthOnline {
		record.LastReceivedAt = now
	}
	if current.State != state {
		record.ChangedAt = now
	}
	r.records[key] = record
	return record
}

func (r *HealthRegistry) ReportOnline(chamberID string, device model.DeviceKind, message string, observedAt, receivedAt time.Time) DeviceHealth {
	return r.report(chamberID, device, HealthOnline, message, observedAt, receivedAt)
}

func (r *HealthRegistry) ReportOffline(chamberID string, device model.DeviceKind, message string, at time.Time) DeviceHealth {
	return r.report(chamberID, device, HealthOffline, message, time.Time{}, at)
}

func (r *HealthRegistry) Get(chamberID string, device model.DeviceKind) (DeviceHealth, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, ok := r.records[healthKey(chamberID, device)]
	return record, ok
}

func (r *HealthRegistry) List() []DeviceHealth {
	r.mu.RLock()
	result := make([]DeviceHealth, 0, len(r.records))
	for _, record := range r.records {
		result = append(result, record)
	}
	r.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].ChamberID == result[j].ChamberID {
			return result[i].Device < result[j].Device
		}
		return result[i].ChamberID < result[j].ChamberID
	})
	return result
}

// AllOnline is true only when the chamber has registered devices and every
// one of them is online; unknown or offline devices return false.
func (r *HealthRegistry) AllOnline(chamberID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	found := false
	for _, record := range r.records {
		if record.ChamberID == chamberID {
			found = true
			if record.State != HealthOnline {
				return false
			}
		}
	}
	return found
}
