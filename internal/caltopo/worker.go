package caltopo

import (
	"context"
	"log/slog"
	"time"

	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/store"
)

type WorkerStore interface {
	PendingDeliveries(context.Context, int) ([]store.Delivery, error)
	MarkDelivered(context.Context, int64) error
	MarkFailed(context.Context, int64, int, time.Time, bool, error) error
}

type Worker struct {
	Store     WorkerStore
	Publisher Publisher
	Logger    *slog.Logger
	Timeout   time.Duration
	wake      chan struct{}
}

func NewWorker(store WorkerStore, publisher Publisher, logger *slog.Logger, timeout time.Duration) *Worker {
	return &Worker{
		Store: store, Publisher: publisher, Logger: logger, Timeout: timeout,
		wake: make(chan struct{}, 1),
	}
}

func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker) Run(ctx context.Context) {
	logger := w.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	w.Wake()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-w.wake:
		}
		for {
			count, err := w.drain(ctx)
			if err != nil {
				logger.Error("processing CalTopo delivery queue", "error", err)
				break
			}
			if count == 0 {
				break
			}
		}
	}
}

func (w *Worker) drain(ctx context.Context) (int, error) {
	deliveries, err := w.Store.PendingDeliveries(ctx, 50)
	if err != nil {
		return 0, err
	}
	for _, delivery := range deliveries {
		timeout := w.Timeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		requestCtx, cancel := context.WithTimeout(ctx, timeout)
		err := w.Publisher.Publish(requestCtx, delivery.Position)
		cancel()
		if err == nil {
			if err := w.Store.MarkDelivered(ctx, delivery.ID); err != nil {
				return 0, err
			}
			continue
		}
		attempts := delivery.Attempts + 1
		delay := min(time.Second*time.Duration(1<<min(attempts, 8)), 5*time.Minute)
		if markErr := w.Store.MarkFailed(ctx, delivery.ID, attempts, time.Now().UTC().Add(delay), false, err); markErr != nil {
			return 0, markErr
		}
		logger := w.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("CalTopo position delivery failed",
			"delivery_id", delivery.ID,
			"source", delivery.Position.SourceID(),
			"attempts", attempts,
			"error", err,
		)
	}
	return len(deliveries), nil
}
