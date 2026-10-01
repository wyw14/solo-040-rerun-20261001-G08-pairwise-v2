package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/wyw14/cry-96/internal/model"
)

type heartbeatInput struct {
	ChamberID  string             `json:"chamber_id"`
	Device     model.DeviceKind   `json:"device"`
	Status     model.DeviceStatus `json:"status"`
	ObservedAt time.Time          `json:"observed_at"`
}

func telemetryHeartbeatHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input heartbeatInput
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		heartbeat := model.DeviceHeartbeat{
			ChamberID: input.ChamberID, Device: input.Device,
			Status: input.Status, ObservedAt: input.ObservedAt,
		}
		verdict, health, err := runtime.Telemetry.Ingest(heartbeat)
		if err != nil {
			if errors.Is(err, model.ErrInvalidHeartbeat) {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"verdict": verdict, "health": health})
	}
}

func telemetryDevicesHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, runtime.Telemetry.List())
	}
}
