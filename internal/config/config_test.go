package config

import (
	"io"
	"testing"
)

func TestParseRequiresSerialDevice(t *testing.T) {
	t.Setenv("MESHTASTIC_SERIAL_DEVICE", "")
	_, err := Parse(nil, io.Discard)
	if err == nil {
		t.Fatal("expected missing serial device error")
	}
}

func TestParseValidatesHostedCalTopo(t *testing.T) {
	t.Setenv("MESHTASTIC_SERIAL_DEVICE", "/dev/test")
	_, err := Parse([]string{"-caltopo", "-caltopo-map", "abc"}, io.Discard)
	if err == nil {
		t.Fatal("expected missing hosted credentials error")
	}
}

func TestParseAllowsLocalCalTopoWithoutCredentials(t *testing.T) {
	t.Setenv("MESHTASTIC_SERIAL_DEVICE", "/dev/test")
	t.Setenv("CALTOPO_ENDPOINT", "http://localhost:8080")
	cfg, err := Parse([]string{"-caltopo", "-caltopo-map", "abc"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CalTopo.Enabled {
		t.Fatal("CalTopo was not enabled")
	}
}

func TestParseHTTPListenAddress(t *testing.T) {
	t.Setenv("MESHTASTIC_SERIAL_DEVICE", "/dev/test")
	t.Setenv("HTTP_LISTEN_ADDRESS", "127.0.0.1:9090")
	cfg, err := Parse(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPListenAddress != "127.0.0.1:9090" {
		t.Fatalf("HTTP listen address=%q", cfg.HTTPListenAddress)
	}
}

func TestParseDebug(t *testing.T) {
	t.Setenv("MESHTASTIC_SERIAL_DEVICE", "/dev/test")
	t.Setenv("BRIDGE_DEBUG", "true")
	cfg, err := Parse(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Debug {
		t.Fatal("debug logging was not enabled")
	}

	t.Setenv("BRIDGE_DEBUG", "false")
	cfg, err = Parse([]string{"-debug"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Debug {
		t.Fatal("-debug did not enable debug logging")
	}
}

func TestParsePositionAppDecoding(t *testing.T) {
	t.Setenv("MESHTASTIC_SERIAL_DEVICE", "/dev/test")
	cfg, err := Parse(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DecodePositionApp {
		t.Fatal("position app decoding should default to enabled")
	}

	t.Setenv("DECODE_POSITION_APP", "false")
	cfg, err = Parse(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DecodePositionApp {
		t.Fatal("position app decoding was not disabled")
	}
}

func TestParseRejectsInvalidEnvironmentValue(t *testing.T) {
	t.Setenv("MESHTASTIC_SERIAL_BAUD", "fast")
	if _, err := Parse([]string{"-list-devices"}, io.Discard); err == nil {
		t.Fatal("expected invalid baud environment error")
	}
}
