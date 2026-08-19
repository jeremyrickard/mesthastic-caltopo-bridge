package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	SerialDevice string
	SerialBaud   int
	DatabasePath string
	ListDevices  bool
	Version      bool
	CalTopo      CalTopo
}

type CalTopo struct {
	Enabled      bool
	Endpoint     string
	MapID        string
	CredentialID string
	Key          string
	AccountID    string
	Group        string
	Timeout      time.Duration
}

func Parse(args []string, stderr io.Writer) (Config, error) {
	serialBaud, err := envInt("MESHTASTIC_SERIAL_BAUD", 115200)
	if err != nil {
		return Config{}, err
	}
	calTopoEnabled, err := envBool("CALTOPO_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	calTopoTimeout, err := envDuration("CALTOPO_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		SerialDevice: env("MESHTASTIC_SERIAL_DEVICE", ""),
		SerialBaud:   serialBaud,
		DatabasePath: env("BRIDGE_DATABASE_PATH", "bridge.db"),
		CalTopo: CalTopo{
			Enabled:      calTopoEnabled,
			Endpoint:     env("CALTOPO_ENDPOINT", "caltopo.com"),
			MapID:        env("CALTOPO_MAP_ID", ""),
			CredentialID: env("CALTOPO_CREDENTIAL_ID", ""),
			Key:          env("CALTOPO_KEY", ""),
			AccountID:    env("CALTOPO_ACCOUNT_ID", ""),
			Group:        env("CALTOPO_GROUP", "mesh"),
			Timeout:      calTopoTimeout,
		},
	}

	fs := flag.NewFlagSet("mesthastic-caltopo-bridge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.SerialDevice, "serial-device", cfg.SerialDevice, "Meshtastic serial device path")
	fs.IntVar(&cfg.SerialBaud, "serial-baud", cfg.SerialBaud, "serial baud rate")
	fs.StringVar(&cfg.DatabasePath, "database", cfg.DatabasePath, "SQLite database path")
	fs.BoolVar(&cfg.ListDevices, "list-devices", false, "list serial devices and exit")
	fs.BoolVar(&cfg.Version, "version", false, "print version and exit")
	fs.BoolVar(&cfg.CalTopo.Enabled, "caltopo", cfg.CalTopo.Enabled, "enable CalTopo live-track publishing")
	fs.StringVar(&cfg.CalTopo.Endpoint, "caltopo-endpoint", cfg.CalTopo.Endpoint, "CalTopo endpoint")
	fs.StringVar(&cfg.CalTopo.MapID, "caltopo-map", cfg.CalTopo.MapID, "CalTopo map ID")
	fs.StringVar(&cfg.CalTopo.Group, "caltopo-group", cfg.CalTopo.Group, "CalTopo fleet group")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if c.ListDevices || c.Version {
		return nil
	}
	var errs []error
	if c.SerialDevice == "" {
		errs = append(errs, errors.New("serial device is required (set -serial-device or MESHTASTIC_SERIAL_DEVICE)"))
	}
	if c.SerialBaud <= 0 {
		errs = append(errs, errors.New("serial baud must be positive"))
	}
	if c.DatabasePath == "" {
		errs = append(errs, errors.New("database path is required"))
	}
	if c.CalTopo.Enabled {
		if c.CalTopo.MapID == "" {
			errs = append(errs, errors.New("CalTopo map ID is required when CalTopo is enabled"))
		} else if len(c.CalTopo.MapID) < 3 || len(c.CalTopo.MapID) > 7 {
			errs = append(errs, errors.New("CalTopo map ID must contain 3 to 7 characters"))
		}
		if c.CalTopo.Group == "" || strings.Contains(c.CalTopo.Group, "-") {
			errs = append(errs, errors.New("CalTopo group must be non-empty and cannot contain '-'"))
		}
		if isHosted(c.CalTopo.Endpoint) && (c.CalTopo.CredentialID == "" || c.CalTopo.Key == "") {
			errs = append(errs, errors.New("CalTopo credential ID and key are required for hosted endpoints"))
		}
		if c.CalTopo.Timeout <= 0 {
			errs = append(errs, errors.New("CalTopo timeout must be positive"))
		}
	}
	return errors.Join(errs...)
}

func isHosted(endpoint string) bool {
	endpoint = strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(endpoint), "https://"), "http://")
	endpoint = strings.Split(endpoint, "/")[0]
	endpoint = strings.Split(endpoint, ":")[0]
	return endpoint == "caltopo.com" || endpoint == "sartopo.com" || endpoint == "testing.caltopo.com"
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
}

func envBool(key string, fallback bool) (bool, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return parsed, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return parsed, nil
}
