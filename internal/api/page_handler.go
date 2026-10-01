package api

import (
	"net/http"
	"os"
	"path/filepath"
)

func pageHandler(webDirectory, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(webDirectory, name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			writeError(w, http.StatusNotFound, os.ErrNotExist)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, path)
	}
}

func healthHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusOK
		summary := "ok"
		for _, chamberID := range runtime.Chambers {
			if !runtime.Health.AllOnline(chamberID) {
				status = http.StatusServiceUnavailable
				summary = "devices_unavailable"
				break
			}
		}
		writeJSON(w, status, map[string]any{
			"status": summary, "request_id": requestID(r.Context()),
			"devices": runtime.Telemetry.Views(), "time": runtime.Now().UTC(),
		})
	}
}
