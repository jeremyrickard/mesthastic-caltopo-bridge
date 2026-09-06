package caltopo

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/model"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/store"
)

func TestWorkerMarksSuccessfulDelivery(t *testing.T) {
	database := &fakeDeliveryStore{
		pending: []store.Delivery{{ID: 7, Position: model.Position{SourceNode: 1}}},
	}
	publisher := &fakePublisher{}
	worker := newTestWorker(database, publisher)
	count, err := worker.drain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || database.delivered != 7 || publisher.calls != 1 {
		t.Fatalf("count=%d delivered=%d calls=%d", count, database.delivered, publisher.calls)
	}
}

func TestWorkerPersistsTransientFailureWithoutAbandoningDelivery(t *testing.T) {
	database := &fakeDeliveryStore{
		pending: []store.Delivery{{ID: 8, Attempts: 7, Position: model.Position{SourceNode: 2}}},
	}
	publisher := &fakePublisher{err: errors.New("offline")}
	worker := newTestWorker(database, publisher)
	if _, err := worker.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if database.failed != 8 || database.terminal || database.attempts != 8 {
		t.Fatalf("failed=%d attempts=%d terminal=%v", database.failed, database.attempts, database.terminal)
	}
}

func TestWorkerFiltersStationaryJitterAndConfirmsMovement(t *testing.T) {
	start := time.Date(2026, time.September, 5, 20, 0, 0, 0, time.UTC)
	database := &fakeDeliveryStore{pending: []store.Delivery{
		deliveryAt(1, start, 38.9282, -104.7010),
		deliveryAt(2, start.Add(time.Minute), 38.92823, -104.70102),
		deliveryAt(3, start.Add(2*time.Minute), 38.92847, -104.7010),
		deliveryAt(4, start.Add(3*time.Minute), 38.92848, -104.7010),
	}}
	publisher := &fakePublisher{}
	worker := newTestWorker(database, publisher)

	if _, err := worker.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if publisher.calls != 2 {
		t.Fatalf("publisher calls=%d, want initial position and confirmed movement", publisher.calls)
	}
	if publisher.positions[1].Latitude != 38.92847 {
		t.Fatalf("filtered latitude=%v", publisher.positions[1].Latitude)
	}
	if len(database.deliveredIDs) != 4 {
		t.Fatalf("delivered IDs=%v", database.deliveredIDs)
	}
}

func TestWorkerPublishesStationaryHeartbeat(t *testing.T) {
	start := time.Date(2026, time.September, 5, 20, 0, 0, 0, time.UTC)
	database := &fakeDeliveryStore{pending: []store.Delivery{
		deliveryAt(1, start, 38.9282, -104.7010),
		deliveryAt(2, start.Add(time.Minute), 38.92821, -104.7010),
		deliveryAt(3, start.Add(5*time.Minute), 38.92819, -104.7010),
	}}
	publisher := &fakePublisher{}
	worker := newTestWorker(database, publisher)

	if _, err := worker.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if publisher.calls != 2 {
		t.Fatalf("publisher calls=%d, want initial position and heartbeat", publisher.calls)
	}
}

func deliveryAt(id int64, sourceTime time.Time, latitude, longitude float64) store.Delivery {
	return store.Delivery{
		ID: id,
		Position: model.Position{
			SourceNode: 1, SourceTime: sourceTime, Latitude: latitude, Longitude: longitude,
		},
	}
}

func newTestWorker(database WorkerStore, publisher Publisher) *Worker {
	return NewWorker(
		database,
		publisher,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		time.Second,
		FilterConfig{MovementMeters: 25, Heartbeat: 5 * time.Minute},
	)
}

type fakePublisher struct {
	calls     int
	positions []model.Position
	err       error
}

func (p *fakePublisher) Publish(_ context.Context, position model.Position) error {
	p.calls++
	p.positions = append(p.positions, position)
	return p.err
}

type fakeDeliveryStore struct {
	pending      []store.Delivery
	delivered    int64
	deliveredIDs []int64
	failed       int64
	attempts     int
	terminal     bool
}

func (s *fakeDeliveryStore) PendingDeliveries(context.Context, int) ([]store.Delivery, error) {
	pending := s.pending
	s.pending = nil
	return pending, nil
}

func (s *fakeDeliveryStore) MarkDelivered(_ context.Context, id int64) error {
	s.delivered = id
	s.deliveredIDs = append(s.deliveredIDs, id)
	return nil
}

func (s *fakeDeliveryStore) MarkFailed(_ context.Context, id int64, attempts int, _ time.Time, terminal bool, _ error) error {
	s.failed = id
	s.attempts = attempts
	s.terminal = terminal
	return nil
}
