package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/model"
)

type stubStore struct {
	positions []model.Position
	err       error
}

func (s stubStore) Positions(context.Context) ([]model.Position, error) {
	return s.positions, s.err
}

func TestMapFitsPositionBounds(t *testing.T) {
	server := NewServer(":0", stubStore{}, nil)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, `fetch("/api/positions"`) ||
		!strings.Contains(body, "map.fitBounds(bounds") {
		t.Fatalf("map page does not load and fit position bounds")
	}
}

func TestPositionsAPI(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	server := NewServer(":0", stubStore{positions: []model.Position{{
		SourceNode: 42, Callsign: "team", Latitude: 40.1, Longitude: -105.2,
		SourceTime: now, ReceivedAt: now,
	}}}, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/positions", nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	var positions []positionResponse
	if err := json.NewDecoder(response.Body).Decode(&positions); err != nil {
		t.Fatal(err)
	}
	if len(positions) != 1 || positions[0].SourceID != "!0000002a" ||
		positions[0].Callsign != "team" {
		t.Fatalf("positions=%+v", positions)
	}
}

func TestPositionsAPIFailure(t *testing.T) {
	server := NewServer(":0", stubStore{err: errors.New("database offline")}, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/positions", nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", response.Code)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "could not load positions") {
		t.Fatalf("body=%q", body)
	}
}
