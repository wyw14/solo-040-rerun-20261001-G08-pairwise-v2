package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/wyw14/cry-96/internal/model"
)

type heartbeatInput struct {
	ChamberID  string    `json:"chamber_id"`
	Device     string    `json:"device"`
	Status     string    `json:"status"`
	ObservedAt time.Time `json:"observed_at"`
	Sequence   uint64    `json:"sequence,omitempty"`
}

func (input heartbeatInput) toModel() (model.DeviceHeartbeat, error) {
	heartbeat := model.DeviceHeartbeat{
		ChamberID: input.ChamberID, Device: model.DeviceKind(input.Device),
		Status: model.DeviceStatus(input.Status), ObservedAt: input.ObservedAt,
		Sequence: input.Sequence,
	}
	if err := heartbeat.Validate(); err != nil {
		return model.DeviceHeartbeat{}, err
	}
	return heartbeat, nil
}

type heartbeatBatchInput struct {
	Heartbeats []heartbeatInput `json:"heartbeats"`
}

// telemetryHeartbeatHandler accepts a batch of device heartbeats. Each item
// is processed independently; late or out-of-order reports are reported as
// rejected but never overwrite newer data.
func telemetryHeartbeatHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input heartbeatBatchInput
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if len(input.Heartbeats) == 0 || len(input.Heartbeats) > 256 {
			writeError(w, http.StatusBadRequest, errors.New("heartbeats must contain 1 to 256 items"))
			return
		}
		heartbeats := make([]model.DeviceHeartbeat, 0, len(input.Heartbeats))
		for _, item := range input.Heartbeats {
			heartbeat, err := item.toModel()
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			heartbeats = append(heartbeats, heartbeat)
		}
		results := runtime.Telemetry.IngestBatch(heartbeats)
		writeJSON(w, http.StatusAccepted, map[string]any{"results": results})
	}
}

// deviceHeartbeatHandler accepts one heartbeat addressed via path params.
func deviceHeartbeatHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input heartbeatInput
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		input.ChamberID = chi.URLParam(r, "chamberID")
		input.Device = chi.URLParam(r, "device")
		heartbeat, err := input.toModel()
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		result := runtime.Telemetry.Ingest(heartbeat)
		if !result.Accepted {
			writeJSON(w, http.StatusConflict, result)
			return
		}
		writeJSON(w, http.StatusAccepted, result)
	}
}

func telemetryDevicesHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, runtime.Telemetry.Views())
	}
}

func chamberDevicesHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, runtime.Telemetry.ChamberViews(chi.URLParam(r, "chamberID")))
	}
}
