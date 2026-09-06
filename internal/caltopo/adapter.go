package caltopo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/jeremyrickard/gotopo"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/config"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/model"
)

type TrackStore interface {
	TrackID(context.Context, string) (string, bool, error)
	SaveTrack(context.Context, string, string, string) error
}

type Adapter struct {
	client *gotopo.Client
	store  TrackStore
	group  string
	mapID  string
	mu     sync.Mutex
}

func New(_ context.Context, cfg config.CalTopo, store TrackStore) (*Adapter, error) {
	httpClient := &http.Client{Timeout: cfg.Timeout}
	client, err := gotopo.NewClient(
		gotopo.WithEndpoint(cfg.Endpoint),
		gotopo.WithHTTPClient(httpClient),
		gotopo.WithCredentials(gotopo.Credentials{
			ID:        cfg.CredentialID,
			Key:       cfg.Key,
			AccountID: cfg.AccountID,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create CalTopo client: %w", err)
	}
	return &Adapter{client: client, store: store, group: cfg.Group, mapID: cfg.MapID}, nil
}

func (a *Adapter) Close() error {
	return a.client.Close()
}

func (a *Adapter) Publish(ctx context.Context, position model.Position) error {
	if err := a.ensureOpen(ctx); err != nil {
		return err
	}
	sourceID := position.SourceID()
	trackID, exists, err := a.store.TrackID(ctx, sourceID)
	if err != nil {
		return err
	}
	if !exists {
		trackID, err = a.createAndSaveTrack(ctx, sourceID, position.Callsign)
		if err != nil {
			return err
		}
	}
	err = a.client.UpdateLiveTrack(ctx, trackID, position.Latitude, position.Longitude, position.Altitude)
	if !errors.Is(err, gotopo.ErrNotFound) {
		return err
	}
	trackID, err = a.createAndSaveTrack(ctx, sourceID, position.Callsign)
	if err != nil {
		return err
	}
	return a.client.UpdateLiveTrack(ctx, trackID, position.Latitude, position.Longitude, position.Altitude)
}

func (a *Adapter) createAndSaveTrack(ctx context.Context, sourceID, title string) (string, error) {
	deviceID := a.group + "-" + strings.TrimPrefix(sourceID, "!")
	trackID, err := a.reconcileOrCreate(ctx, deviceID, title)
	if err != nil {
		return "", err
	}
	if err := a.store.SaveTrack(ctx, sourceID, deviceID, trackID); err != nil {
		return "", err
	}
	return trackID, nil
}

func (a *Adapter) ensureOpen(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client.MapID() == a.mapID {
		return nil
	}
	if err := a.client.OpenMap(ctx, a.mapID, gotopo.OpenMapOptions{}); err != nil {
		return fmt.Errorf("open CalTopo map: %w", err)
	}
	return nil
}

func (a *Adapter) reconcileOrCreate(ctx context.Context, deviceID, title string) (string, error) {
	features, err := a.client.GetFeatures(ctx, gotopo.FeatureFilter{Class: "LiveTrack"})
	if err != nil {
		return "", fmt.Errorf("list CalTopo live tracks: %w", err)
	}
	fullDeviceID := "FLEET:" + deviceID
	for _, feature := range features {
		if feature.Properties.String("deviceId") == fullDeviceID {
			return feature.ID, nil
		}
	}
	if strings.TrimSpace(title) == "" {
		title = deviceID
	}
	feature, err := a.client.AddLiveTrack(ctx, gotopo.LiveTrackOptions{
		Title: title, DeviceID: deviceID,
	})
	if err != nil {
		return "", fmt.Errorf("create CalTopo live track: %w", err)
	}
	return feature.ID, nil
}

type Publisher interface {
	Publish(context.Context, model.Position) error
}
