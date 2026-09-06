package caltopo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/config"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/model"
)

func TestAdapterCreatesAndUpdatesLiveTrack(t *testing.T) {
	var mu sync.Mutex
	createCalls := 0
	updateCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/since/0":
			io.WriteString(w, `{"status":"ok","result":{"ids":{},"state":{"features":[]},"timestamp":1}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/LiveTrack":
			body, _ := io.ReadAll(r.Body)
			form, _ := url.ParseQuery(string(body))
			var feature map[string]any
			if err := json.Unmarshal([]byte(form.Get("json")), &feature); err != nil {
				t.Error(err)
			}
			properties, _ := feature["properties"].(map[string]any)
			if properties["deviceId"] != "FLEET:mesh-0000002a" {
				t.Errorf("deviceId=%v", properties["deviceId"])
			}
			properties["class"] = "LiveTrack"
			feature["id"] = "track-1"
			response, _ := json.Marshal(map[string]any{"status": "ok", "result": feature})
			mu.Lock()
			createCalls++
			mu.Unlock()
			w.Write(response)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/position/report/mesh":
			if r.URL.Query().Get("id") != "0000002a" ||
				r.URL.Query().Get("lat") != "40" ||
				r.URL.Query().Get("lng") != "-105" {
				t.Errorf("unexpected position query: %s", r.URL.RawQuery)
			}
			mu.Lock()
			updateCalls++
			mu.Unlock()
			io.WriteString(w, `{}`)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()

	tracks := &memoryTrackStore{}
	adapter, err := New(context.Background(), config.CalTopo{
		Endpoint: server.URL, MapID: "ABC", Group: "mesh", Timeout: time.Second,
	}, tracks)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	position := model.Position{
		SourceNode: 42, Callsign: "Rescue 42", Latitude: 40, Longitude: -105,
	}
	if err := adapter.Publish(context.Background(), position); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Publish(context.Background(), position); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if createCalls != 1 || updateCalls != 2 {
		t.Fatalf("create calls=%d update calls=%d", createCalls, updateCalls)
	}
	if tracks.trackID != "track-1" || !strings.HasSuffix(tracks.deviceID, "0000002a") {
		t.Fatalf("unexpected mapping: %+v", tracks)
	}
}

func TestAdapterRecreatesMissingStoredLiveTrack(t *testing.T) {
	var created bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/since/0":
			io.WriteString(w, `{"status":"ok","result":{"ids":{},"state":{"features":[]},"timestamp":1}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/LiveTrack":
			created = true
			io.WriteString(w, `{"status":"ok","result":{"type":"Feature","id":"track-new","properties":{"class":"LiveTrack","title":"Rescue 42","deviceId":"FLEET:mesh-0000002a"}}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/position/report/mesh":
			io.WriteString(w, `{}`)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()

	tracks := &memoryTrackStore{
		sourceID: "!0000002a",
		deviceID: "mesh-0000002a",
		trackID:  "track-deleted",
	}
	adapter, err := New(context.Background(), config.CalTopo{
		Endpoint: server.URL, MapID: "ABC", Group: "mesh", Timeout: time.Second,
	}, tracks)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()

	position := model.Position{
		SourceNode: 42, Callsign: "Rescue 42", Latitude: 40, Longitude: -105,
	}
	if err := adapter.Publish(context.Background(), position); err != nil {
		t.Fatal(err)
	}
	if !created || tracks.trackID != "track-new" {
		t.Fatalf("created=%v mapping=%+v", created, tracks)
	}
}

type memoryTrackStore struct {
	sourceID string
	deviceID string
	trackID  string
}

func (s *memoryTrackStore) TrackID(_ context.Context, sourceID string) (string, bool, error) {
	if s.sourceID == sourceID && s.trackID != "" {
		return s.trackID, true, nil
	}
	return "", false, nil
}

func (s *memoryTrackStore) SaveTrack(_ context.Context, sourceID, deviceID, trackID string) error {
	s.sourceID, s.deviceID, s.trackID = sourceID, deviceID, trackID
	return nil
}
