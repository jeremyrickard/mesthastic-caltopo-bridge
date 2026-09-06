package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/caltopo"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/config"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/ingest"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/meshtastic"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/store"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/web"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("bridge stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Parse(os.Args[1:], os.Stderr)
	if err != nil {
		return err
	}
	if cfg.Version {
		fmt.Printf("meshtastic-caltopo-bridge %s (%s) %s/%s\n", version, commit, runtime.GOOS, runtime.GOARCH)
		return nil
	}
	if cfg.ListDevices {
		return printDevices()
	}
	if _, err := os.Stat(cfg.SerialDevice); err != nil {
		return fmt.Errorf("access serial device %s: %w", cfg.SerialDevice, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	database, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer database.Close()

	var worker *caltopo.Worker
	var adapter *caltopo.Adapter
	var wait sync.WaitGroup
	if cfg.CalTopo.Enabled {
		adapter, err = caltopo.New(ctx, cfg.CalTopo, database)
		if err != nil {
			return err
		}
		defer adapter.Close()
		worker = caltopo.NewWorker(database, adapter, logger, cfg.CalTopo.Timeout)
		wait.Add(1)
		go func() {
			defer wait.Done()
			worker.Run(ctx)
		}()
	}

	service := &ingest.Service{
		Store:             database,
		Nodes:             database,
		EnqueueCalTopo:    cfg.CalTopo.Enabled,
		DecodePositionApp: cfg.DecodePositionApp,
		Logger:            logger,
	}
	if worker != nil {
		service.WakeDeliveries = worker.Wake
	}
	source := meshtastic.NewSerialSource(cfg.SerialDevice, cfg.SerialBaud, cfg.Debug, logger, service.Handle)
	httpServer := web.NewServer(cfg.HTTPListenAddress, database, logger)
	logger.Info("starting bridge",
		"version", version,
		"commit", commit,
		"serial_device", cfg.SerialDevice,
		"database", cfg.DatabasePath,
		"http_listen_address", cfg.HTTPListenAddress,
		"caltopo_enabled", cfg.CalTopo.Enabled,
		"decode_position_app", cfg.DecodePositionApp,
		"debug", cfg.Debug,
	)

	runtimeErrors := make(chan error, 2)
	wait.Add(2)
	go func() {
		defer wait.Done()
		runtimeErrors <- source.Run(ctx)
	}()
	go func() {
		defer wait.Done()
		logger.Info("serving position map", "address", cfg.HTTPListenAddress)
		serverErr := httpServer.ListenAndServe()
		if errors.Is(serverErr, http.ErrServerClosed) {
			serverErr = nil
		}
		runtimeErrors <- serverErr
	}()

	err = <-runtimeErrors
	stop()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	shutdownErr := httpServer.Shutdown(shutdownCtx)
	wait.Wait()
	return errors.Join(err, shutdownErr)
}

func printDevices() error {
	devices, err := meshtastic.ListDevices()
	if err != nil {
		return fmt.Errorf("list serial devices: %w", err)
	}
	slices.Sort(devices)
	if len(devices) == 0 {
		return errors.New("no serial devices found")
	}
	for _, device := range devices {
		fmt.Println(device)
	}
	return nil
}
