package caltopo

import (
	"context"
	"log/slog"
	"math"
	"slices"
	"time"

	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/model"
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
	filter    *positionFilter
}

type FilterConfig struct {
	MovementMeters float64
	Heartbeat      time.Duration
}

func NewWorker(store WorkerStore, publisher Publisher, logger *slog.Logger, timeout time.Duration, filterConfig FilterConfig) *Worker {
	return &Worker{
		Store: store, Publisher: publisher, Logger: logger, Timeout: timeout,
		wake: make(chan struct{}, 1), filter: newPositionFilter(filterConfig),
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
		position, publish := w.filter.prepare(delivery)
		if !publish {
			if err := w.Store.MarkDelivered(ctx, delivery.ID); err != nil {
				return 0, err
			}
			w.filter.complete(delivery.ID, false)
			continue
		}
		timeout := w.Timeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		requestCtx, cancel := context.WithTimeout(ctx, timeout)
		err := w.Publisher.Publish(requestCtx, position)
		cancel()
		if err == nil {
			if err := w.Store.MarkDelivered(ctx, delivery.ID); err != nil {
				return 0, err
			}
			w.filter.complete(delivery.ID, true)
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

type filteredDelivery struct {
	position model.Position
	publish  bool
}

type positionFilter struct {
	nodes          map[uint32]*positionFilterState
	prepared       map[int64]filteredDelivery
	movementMeters float64
	heartbeat      time.Duration
}

type positionFilterState struct {
	samples       []model.Position
	lastPublished *model.Position
}

func newPositionFilter(config FilterConfig) *positionFilter {
	return &positionFilter{
		nodes:          make(map[uint32]*positionFilterState),
		prepared:       make(map[int64]filteredDelivery),
		movementMeters: config.MovementMeters,
		heartbeat:      config.Heartbeat,
	}
}

func (f *positionFilter) prepare(delivery store.Delivery) (model.Position, bool) {
	if prepared, ok := f.prepared[delivery.ID]; ok {
		return prepared.position, prepared.publish
	}
	state := f.nodes[delivery.Position.SourceNode]
	if state == nil {
		state = &positionFilterState{}
		f.nodes[delivery.Position.SourceNode] = state
	}
	state.samples = append(state.samples, delivery.Position)
	if len(state.samples) > 3 {
		state.samples = state.samples[len(state.samples)-3:]
	}

	position := delivery.Position
	publish := state.lastPublished == nil
	if !publish && delivery.Position.SourceTime.Sub(state.lastPublished.SourceTime) >= f.heartbeat {
		publish = true
	}
	if len(state.samples) == 3 {
		position.Latitude = medianCoordinate(state.samples, func(position model.Position) float64 {
			return position.Latitude
		})
		position.Longitude = medianCoordinate(state.samples, func(position model.Position) float64 {
			return position.Longitude
		})
		if !publish {
			publish = distanceMeters(*state.lastPublished, position) >= f.movementMeters
		}
	}
	f.prepared[delivery.ID] = filteredDelivery{position: position, publish: publish}
	return position, publish
}

func (f *positionFilter) complete(deliveryID int64, published bool) {
	prepared := f.prepared[deliveryID]
	if published {
		position := prepared.position
		f.nodes[position.SourceNode].lastPublished = &position
	}
	delete(f.prepared, deliveryID)
}

func medianCoordinate(positions []model.Position, coordinate func(model.Position) float64) float64 {
	values := make([]float64, len(positions))
	for index, position := range positions {
		values[index] = coordinate(position)
	}
	slices.Sort(values)
	return values[len(values)/2]
}

func distanceMeters(left, right model.Position) float64 {
	const earthRadiusMeters = 6371000.0
	latitude1 := left.Latitude * math.Pi / 180
	latitude2 := right.Latitude * math.Pi / 180
	latitudeDelta := (right.Latitude - left.Latitude) * math.Pi / 180
	longitudeDelta := (right.Longitude - left.Longitude) * math.Pi / 180
	haversine := math.Sin(latitudeDelta/2)*math.Sin(latitudeDelta/2) +
		math.Cos(latitude1)*math.Cos(latitude2)*
			math.Sin(longitudeDelta/2)*math.Sin(longitudeDelta/2)
	haversine = math.Min(1, haversine)
	return earthRadiusMeters * 2 * math.Atan2(math.Sqrt(haversine), math.Sqrt(1-haversine))
}
