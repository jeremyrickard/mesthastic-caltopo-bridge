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
	worker := NewWorker(database, publisher, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
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
	worker := NewWorker(database, publisher, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
	if _, err := worker.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if database.failed != 8 || database.terminal || database.attempts != 8 {
		t.Fatalf("failed=%d attempts=%d terminal=%v", database.failed, database.attempts, database.terminal)
	}
}

type fakePublisher struct {
	calls int
	err   error
}

func (p *fakePublisher) Publish(context.Context, model.Position) error {
	p.calls++
	return p.err
}

type fakeDeliveryStore struct {
	pending   []store.Delivery
	delivered int64
	failed    int64
	attempts  int
	terminal  bool
}

func (s *fakeDeliveryStore) PendingDeliveries(context.Context, int) ([]store.Delivery, error) {
	pending := s.pending
	s.pending = nil
	return pending, nil
}

func (s *fakeDeliveryStore) MarkDelivered(_ context.Context, id int64) error {
	s.delivered = id
	return nil
}

func (s *fakeDeliveryStore) MarkFailed(_ context.Context, id int64, attempts int, _ time.Time, terminal bool, _ error) error {
	s.failed = id
	s.attempts = attempts
	s.terminal = terminal
	return nil
}
