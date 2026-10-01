package model

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidHeartbeat marks malformed telemetry input (client-side fault).
var ErrInvalidHeartbeat = errors.New("invalid device heartbeat")

// DeviceStatus is the running condition reported by gate and valve telemetry.
type DeviceStatus string

const (
	DeviceStatusRunning DeviceStatus = "running"
	DeviceStatusStopped DeviceStatus = "stopped"
	DeviceStatusFault   DeviceStatus = "fault"
)

func (s DeviceStatus) Valid() bool {
	switch s {
	case DeviceStatusRunning, DeviceStatusStopped, DeviceStatusFault:
		return true
	}
	return false
}

// DeviceHeartbeat is one telemetry sample from a gate or valve controller.
// ObservedAt is stamped by the device; ReceivedAt is stamped on arrival.
type DeviceHeartbeat struct {
	ChamberID  string       `json:"chamber_id"`
	Device     DeviceKind   `json:"device"`
	Status     DeviceStatus `json:"status"`
	ObservedAt time.Time    `json:"observed_at"`
	ReceivedAt time.Time    `json:"received_at"`
}

func (h DeviceHeartbeat) Validate() error {
	if h.ChamberID == "" {
		return fmt.Errorf("%w: chamber is required", ErrInvalidHeartbeat)
	}
	switch h.Device {
	case DeviceUpstreamGate, DeviceDownstreamGate, DeviceFillValve, DeviceDrainValve:
	default:
		return fmt.Errorf("%w: device is not a gate or valve", ErrInvalidHeartbeat)
	}
	if !h.Status.Valid() {
		return fmt.Errorf("%w: status is invalid", ErrInvalidHeartbeat)
	}
	if h.ObservedAt.IsZero() {
		return fmt.Errorf("%w: observation time is required", ErrInvalidHeartbeat)
	}
	return nil
}
