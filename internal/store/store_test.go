package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/model"
)

func TestArchiveDeduplicatesPositionsButRetainsPackets(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Second)
	packet := model.Packet{
		From: 1, MeshPacketID: 2, Port: 72, ReceivedAt: now,
		RawPacket: []byte{1}, RawPayload: []byte{2}, ParseStatus: "position",
	}
	position := &model.Position{
		SourceNode: 1, MeshPacketID: 2, Callsign: "one",
		Latitude: 40, Longitude: -105, SourceTime: now, ReceivedAt: now,
	}
	_, positionID, inserted, err := database.Archive(ctx, packet, position, true)
	if err != nil || !inserted || positionID == 0 {
		t.Fatalf("first archive: id=%d inserted=%v err=%v", positionID, inserted, err)
	}
	if _, _, inserted, err := database.Archive(ctx, packet, position, true); err != nil || inserted {
		t.Fatalf("duplicate archive: inserted=%v err=%v", inserted, err)
	}
	var packetCount, positionCount int
	if err := database.db.QueryRow("SELECT COUNT(*) FROM mesh_packets").Scan(&packetCount); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow("SELECT COUNT(*) FROM tak_positions").Scan(&positionCount); err != nil {
		t.Fatal(err)
	}
	if packetCount != 2 || positionCount != 1 {
		t.Fatalf("packets=%d positions=%d", packetCount, positionCount)
	}
	laterPacket := packet
	laterPacket.ReceivedAt = laterPacket.ReceivedAt.Add(16 * time.Minute)
	laterPosition := *position
	laterPosition.ReceivedAt = laterPacket.ReceivedAt
	laterPosition.SourceTime = laterPacket.ReceivedAt
	if _, _, inserted, err := database.Archive(ctx, laterPacket, &laterPosition, true); err != nil || !inserted {
		t.Fatalf("reused packet ID after dedupe window: inserted=%v err=%v", inserted, err)
	}
	deliveries, err := database.PendingDeliveries(ctx, 10)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("deliveries=%d err=%v", len(deliveries), err)
	}
	if err := database.MarkDelivered(ctx, deliveries[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkDelivered(ctx, deliveries[0].ID); err == nil {
		t.Fatal("second delivery completion unexpectedly succeeded")
	}
}

func TestDeliveryFailureAndTrackMapping(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}

	defer database.Close()
	now := time.Now().UTC().Add(-time.Minute)
	packet := model.Packet{
		From: 3, MeshPacketID: 4, Port: 72, ReceivedAt: now,
		RawPacket: []byte{1}, RawPayload: []byte{2}, ParseStatus: "position",
	}
	position := &model.Position{
		SourceNode: 3, MeshPacketID: 4, Callsign: "three",
		Latitude: 39, Longitude: -104, SourceTime: now, ReceivedAt: now,
	}
	if _, _, _, err := database.Archive(ctx, packet, position, true); err != nil {
		t.Fatal(err)
	}
	deliveries, err := database.PendingDeliveries(ctx, 1)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("deliveries=%v err=%v", deliveries, err)
	}
	retryAt := time.Now().UTC().Add(time.Hour)
	if err := database.MarkFailed(ctx, deliveries[0].ID, 1, retryAt, false, errors.New("offline")); err != nil {
		t.Fatal(err)
	}
	deliveries, err = database.PendingDeliveries(ctx, 1)
	if err != nil || len(deliveries) != 0 {
		t.Fatalf("backoff delivery returned: %v err=%v", deliveries, err)
	}
	if err := database.SaveTrack(ctx, "!00000003", "mesh-00000003", "track-1"); err != nil {
		t.Fatal(err)
	}
	trackID, found, err := database.TrackID(ctx, "!00000003")
	if err != nil || !found || trackID != "track-1" {
		t.Fatalf("track=%q found=%v err=%v", trackID, found, err)
	}
}

func TestArchiveStoresEmptyPayloadAsBlob(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	packet := model.Packet{
		From: 9, ReceivedAt: time.Now().UTC(), ParseStatus: "non_tak",
	}
	packetID, _, _, err := database.Archive(ctx, packet, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	var valueType string
	var length int
	if err := database.db.QueryRow(
		"SELECT typeof(raw_payload), length(raw_payload) FROM mesh_packets WHERE id = ?",
		packetID,
	).Scan(&valueType, &length); err != nil {
		t.Fatal(err)
	}
	if valueType != "blob" || length != 0 {
		t.Fatalf("type=%q length=%d", valueType, length)
	}
}
