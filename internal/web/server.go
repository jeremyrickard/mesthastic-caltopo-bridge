package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/model"
)

type PositionStore interface {
	Positions(context.Context) ([]model.Position, error)
}

type positionResponse struct {
	SourceID       string    `json:"source_id"`
	SourcePort     int32     `json:"source_port"`
	Callsign       string    `json:"callsign"`
	DeviceCallsign string    `json:"device_callsign,omitempty"`
	Latitude       float64   `json:"latitude"`
	Longitude      float64   `json:"longitude"`
	Altitude       *float64  `json:"altitude,omitempty"`
	Speed          *float64  `json:"speed,omitempty"`
	Course         *float64  `json:"course,omitempty"`
	LocationSource string    `json:"location_source,omitempty"`
	PrecisionBits  uint32    `json:"precision_bits,omitempty"`
	SourceTime     time.Time `json:"source_time"`
	ReceivedAt     time.Time `json:"received_at"`
}

func NewServer(address string, store PositionStore, logger *slog.Logger) *http.Server {
	if logger == nil {
		logger = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", serveMap)
	mux.HandleFunc("GET /api/positions", servePositions(store, logger))
	return &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func serveMap(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = writer.Write([]byte(mapHTML))
}

func servePositions(store PositionStore, logger *slog.Logger) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		positions, err := store.Positions(request.Context())
		if err != nil {
			logger.Error("querying positions for map", "error", err)
			http.Error(writer, "could not load positions", http.StatusInternalServerError)
			return
		}
		response := make([]positionResponse, 0, len(positions))
		for _, position := range positions {
			response = append(response, positionResponse{
				SourceID:       position.SourceID(),
				SourcePort:     position.SourcePort,
				Callsign:       position.Callsign,
				DeviceCallsign: position.DeviceCallsign,
				Latitude:       position.Latitude,
				Longitude:      position.Longitude,
				Altitude:       position.Altitude,
				Speed:          position.Speed,
				Course:         position.Course,
				LocationSource: position.LocationSource,
				PrecisionBits:  position.PrecisionBits,
				SourceTime:     position.SourceTime,
				ReceivedAt:     position.ReceivedAt,
			})
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		if err := json.NewEncoder(writer).Encode(response); err != nil &&
			!errors.Is(err, context.Canceled) {
			logger.Error("encoding positions for map", "error", fmt.Errorf("encode positions: %w", err))
		}
	}
}

const mapHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Meshtastic Positions</title>
  <link rel="stylesheet" href="https://unpkg.com/leaflet@1.9.4/dist/leaflet.css"
    integrity="sha256-p4NxAoJBhIINfQ3yn7tNqOxLMv0Y3ebmEoZkzQjW0pM=" crossorigin="">
  <style>
    html, body, #map { height: 100%; margin: 0; }
    .status {
      position: absolute; z-index: 1000; top: 12px; left: 50%;
      transform: translateX(-50%); padding: 8px 12px;
      border-radius: 4px; background: rgb(255 255 255 / 90%);
      font: 14px/1.3 system-ui, sans-serif; box-shadow: 0 1px 5px rgb(0 0 0 / 35%);
    }
  </style>
</head>
<body>
  <div id="map"></div>
  <div id="status" class="status">Loading positions...</div>
  <script src="https://unpkg.com/leaflet@1.9.4/dist/leaflet.js"
    integrity="sha256-20nQCchB9co0qIjJZRGuk2/Z9VM+kNiyxNV1lvTlZBo=" crossorigin=""></script>
  <script>
    const map = L.map("map").setView([20, 0], 2);
    L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
      maxZoom: 19,
      attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>'
    }).addTo(map);

    const status = document.getElementById("status");
    fetch("/api/positions", { cache: "no-store" })
      .then(response => {
        if (!response.ok) throw new Error("HTTP " + response.status);
        return response.json();
      })
      .then(positions => {
        const bounds = [];
        for (const position of positions) {
          const point = [position.latitude, position.longitude];
          const marker = L.circleMarker(point, {
            radius: 6, weight: 2, color: "#1d4ed8", fillColor: "#60a5fa", fillOpacity: 0.8
          }).addTo(map);
          const popup = document.createElement("div");
          const title = document.createElement("strong");
          title.textContent = position.callsign || position.source_id;
          popup.appendChild(title);
          for (const value of [
            position.source_id,
            new Date(position.source_time).toLocaleString(),
            position.altitude == null ? "" : "Altitude: " + position.altitude + " m"
          ]) {
            if (!value) continue;
            popup.appendChild(document.createElement("br"));
            popup.appendChild(document.createTextNode(value));
          }
          marker.bindPopup(popup);
          bounds.push(point);
        }
        if (bounds.length === 1) {
          map.setView(bounds[0], 16);
          status.hidden = true;
        } else if (bounds.length > 1) {
          map.fitBounds(bounds, { padding: [30, 30], maxZoom: 16 });
          status.hidden = true;
        } else {
          status.textContent = "No positions received yet";
        }
      })
      .catch(error => {
        status.textContent = "Unable to load positions";
        console.error(error);
      });
  </script>
</body>
</html>
`
