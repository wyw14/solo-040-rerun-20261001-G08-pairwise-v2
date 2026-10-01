package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type DeviceKind string

const (
	DeviceUpstreamGate   DeviceKind = "upstream_gate"
	DeviceDownstreamGate DeviceKind = "downstream_gate"
	DeviceFillValve      DeviceKind = "fill_valve"
	DeviceDrainValve     DeviceKind = "drain_valve"
)

func (k DeviceKind) Valid() bool {
	switch k {
	case DeviceUpstreamGate, DeviceDownstreamGate, DeviceFillValve, DeviceDrainValve:
		return true
	default:
		return false
	}
}

// GateDirection maps a gate device back to its chamber direction.
// Valve devices return false.
func (k DeviceKind) GateDirection() (Direction, bool) {
	switch k {
	case DeviceUpstreamGate:
		return DirectionUpstream, true
	case DeviceDownstreamGate:
		return DirectionDownstream, true
	default:
		return "", false
	}
}

type CommandState string

const (
	CommandQueued    CommandState = "queued"
	CommandAccepted  CommandState = "accepted"
	CommandExecuting CommandState = "executing"
	CommandSettled   CommandState = "settled"
	CommandCancelled CommandState = "cancelled"
	CommandFailed    CommandState = "failed"
)

func (s CommandState) Terminal() bool {
	return s == CommandSettled || s == CommandCancelled || s == CommandFailed
}

type DeviceCommand struct {
	ID         uuid.UUID    `json:"id"`
	CycleID    uuid.UUID    `json:"cycle_id"`
	ChamberID  string       `json:"chamber_id"`
	Generation uint64       `json:"generation"`
	Epoch      uint64       `json:"epoch"`
	Device     DeviceKind   `json:"device"`
	Action     string       `json:"action"`
	Target     float64      `json:"target,omitempty"`
	State      CommandState `json:"state"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
}

func NewDeviceCommand(cycle TransitCycle, epoch uint64, device DeviceKind, action string, target float64, now time.Time) (DeviceCommand, error) {
	if cycle.ID == uuid.Nil || cycle.ChamberID == "" {
		return DeviceCommand{}, errors.New("command requires a transit cycle")
	}
	if epoch == 0 {
		return DeviceCommand{}, errors.New("command epoch must be positive")
	}
	if device == "" || action == "" {
		return DeviceCommand{}, errors.New("device and action are required")
	}
	return DeviceCommand{
		ID: uuid.New(), CycleID: cycle.ID, ChamberID: cycle.ChamberID,
		Generation: cycle.Generation, Epoch: epoch, Device: device,
		Action: action, Target: target, State: CommandQueued,
		CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}, nil
}

func (c *DeviceCommand) SetState(next CommandState, now time.Time) error {
	if c.State.Terminal() {
		return fmt.Errorf("command %s is already terminal", c.ID)
	}
	c.State = next
	c.UpdatedAt = now.UTC()
	return nil
}

type DeviceAck struct {
	CommandID  uuid.UUID  `json:"command_id"`
	CycleID    uuid.UUID  `json:"cycle_id"`
	ChamberID  string     `json:"chamber_id"`
	Generation uint64     `json:"generation"`
	Epoch      uint64     `json:"epoch"`
	Device     DeviceKind `json:"device"`
	Settled    bool       `json:"settled"`
	ObservedAt time.Time  `json:"observed_at"`
}

func (a DeviceAck) Matches(command DeviceCommand) bool {
	return a.CommandID == command.ID && a.CycleID == command.CycleID &&
		a.Generation == command.Generation && a.Epoch == command.Epoch &&
		a.ChamberID == command.ChamberID && a.Device == command.Device
}

type GatePosition string

const (
	GateOpen    GatePosition = "open"
	GateOpening GatePosition = "opening"
	GateClosed  GatePosition = "closed"
	GateClosing GatePosition = "closing"
)

func (p GatePosition) Sealed() bool { return p == GateClosed }

type LevelSample struct {
	CycleID    uuid.UUID `json:"cycle_id"`
	Generation uint64    `json:"generation"`
	Meters     float64   `json:"meters"`
	ObservedAt time.Time `json:"observed_at"`
}

type DeviceStatus string

const (
	StatusRunning DeviceStatus = "running"
	StatusStopped DeviceStatus = "stopped"
	StatusFault   DeviceStatus = "fault"
)

func (s DeviceStatus) Valid() bool {
	return s == StatusRunning || s == StatusStopped || s == StatusFault
}

// DeviceHeartbeat is a telemetry report emitted by field equipment.
// ObservedAt is the device-side timestamp; Sequence is a per-device
// monotonically increasing counter used to detect late or out-of-order
// delivery. Both are optional but, when present, stale reports must not
// overwrite newer observations.
type DeviceHeartbeat struct {
	Device     DeviceKind   `json:"device"`
	ChamberID  string       `json:"chamber_id"`
	Status     DeviceStatus `json:"status"`
	ObservedAt time.Time    `json:"observed_at"`
	Sequence   uint64       `json:"sequence,omitempty"`
}

func (h DeviceHeartbeat) Validate() error {
	if h.ChamberID == "" {
		return errors.New("heartbeat requires a chamber_id")
	}
	if !h.Device.Valid() {
		return errors.New("heartbeat device is invalid")
	}
	if !h.Status.Valid() {
		return errors.New("heartbeat status is invalid")
	}
	if h.ObservedAt.IsZero() {
		return errors.New("heartbeat observed_at is required")
	}
	return nil
}

// DeviceObservation is the accepted latest view of one piece of equipment.
type DeviceObservation struct {
	ChamberID  string       `json:"chamber_id"`
	Device     DeviceKind   `json:"device"`
	Status     DeviceStatus `json:"status"`
	ObservedAt time.Time    `json:"observed_at"`
	ReceivedAt time.Time    `json:"received_at"`
	Sequence   uint64       `json:"sequence,omitempty"`
}
