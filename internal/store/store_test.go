package store

import (
	"context"
	"database/sql"
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
	positions, err := database.Positions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 1 || positions[0].Callsign != "one" ||
		positions[0].Latitude != 40 || positions[0].Longitude != -105 {
		t.Fatalf("positions=%+v", positions)
	}
	laterPacket := packet
	laterPacket.ReceivedAt = laterPacket.ReceivedAt.Add(16 * time.Minute)
	laterPosition := *position
	laterPosition.ReceivedAt = laterPacket.ReceivedAt
	laterPosition.SourceTime = laterPacket.ReceivedAt
	if _, _, inserted, err := database.Archive(ctx, laterPacket, &laterPosition, true); err != nil || !inserted {
		t.Fatalf("reused packet ID after dedupe window: inserted=%v err=%v", inserted, err)
	}
	positions, err = database.Positions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 1 || !positions[0].SourceTime.Equal(laterPosition.SourceTime) {
		t.Fatalf("latest positions=%+v", positions)
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

func TestArchiveDeduplicatesSameFixAcrossPositionPorts(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	fixTime := time.Now().UTC().Truncate(time.Second)
	for _, sourcePort := range []int32{3, 72} {
		packet := model.Packet{
			From: 7, MeshPacketID: uint32(sourcePort), Port: sourcePort,
			ReceivedAt: fixTime, RawPayload: []byte{byte(sourcePort)}, ParseStatus: "position",
		}
		position := &model.Position{
			SourceNode: 7, MeshPacketID: uint32(sourcePort), SourcePort: sourcePort,
			Callsign: "!00000007", Latitude: 40, Longitude: -105,
			SourceTime: fixTime, ReceivedAt: fixTime,
		}
		if _, _, _, err := database.Archive(ctx, packet, position, true); err != nil {
			t.Fatal(err)
		}
	}

	var positionCount, deliveryCount int
	if err := database.db.QueryRow("SELECT COUNT(*) FROM tak_positions").Scan(&positionCount); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow("SELECT COUNT(*) FROM caltopo_deliveries").Scan(&deliveryCount); err != nil {
		t.Fatal(err)
	}
	if positionCount != 1 || deliveryCount != 1 {
		t.Fatalf("positions=%d deliveries=%d", positionCount, deliveryCount)
	}
}

func TestArchiveStoresPositionAppMetadata(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Now().UTC()
	packet := model.Packet{From: 8, MeshPacketID: 9, Port: 3, ReceivedAt: now, ParseStatus: "position"}
	altitude := 1700.0
	position := &model.Position{
		SourceNode: 8, MeshPacketID: 9, SourcePort: 3, Callsign: "!00000008",
		Latitude: 39, Longitude: -104, Altitude: &altitude,
		LocationSource: "LOC_EXTERNAL", PrecisionBits: 12,
		SourceTime: now, ReceivedAt: now,
	}
	if _, _, inserted, err := database.Archive(ctx, packet, position, false); err != nil || !inserted {
		t.Fatalf("inserted=%v err=%v", inserted, err)
	}
	positions, err := database.Positions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 1 || positions[0].SourcePort != 3 ||
		positions[0].LocationSource != "LOC_EXTERNAL" ||
		positions[0].PrecisionBits != 12 ||
		positions[0].Altitude == nil || *positions[0].Altitude != altitude {
		t.Fatalf("positions=%+v", positions)
	}
}

func TestPositionsReturnsEmptySlice(t *testing.T) {
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	positions, err := database.Positions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if positions == nil || len(positions) != 0 {
		t.Fatalf("positions=%v", positions)
	}
}

func TestNodeCallsignPrefersShortNameAndUpdatesFallbackPositions(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Now().UTC()
	node := uint32(0x12f47fb2)
	packet := model.Packet{
		From: node, MeshPacketID: 1, Port: 3, ReceivedAt: now, ParseStatus: "position",
	}
	position := &model.Position{
		SourceNode: node, MeshPacketID: 1, SourcePort: 3, Callsign: model.NodeID(node),
		Latitude: 38.9, Longitude: -104.7, SourceTime: now, ReceivedAt: now,
	}
	if _, _, _, err := database.Archive(ctx, packet, position, false); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertNode(ctx, node, "R12", "Rescue Twelve"); err != nil {
		t.Fatal(err)
	}
	callsign, err := database.NodeCallsign(ctx, node)
	if err != nil || callsign != "R12" {
		t.Fatalf("callsign=%q err=%v", callsign, err)
	}
	positions, err := database.Positions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 1 || positions[0].Callsign != "R12" {
		t.Fatalf("positions=%+v", positions)
	}

	if err := database.UpsertNode(ctx, node, "", "Rescue Twelve"); err != nil {
		t.Fatal(err)
	}
	callsign, err = database.NodeCallsign(ctx, node)
	if err != nil || callsign != "Rescue Twelve" {
		t.Fatalf("fallback callsign=%q err=%v", callsign, err)
	}
}

func TestOpenMigratesPositionMetadataColumns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bridge.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
		INSERT INTO schema_migrations VALUES (1, '2026-01-01T00:00:00Z');
		CREATE TABLE tak_positions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			packet_id INTEGER NOT NULL,
			dedupe_key TEXT NOT NULL UNIQUE,
			source_node INTEGER NOT NULL,
			mesh_packet_id INTEGER NOT NULL,
			callsign TEXT NOT NULL,
			device_callsign TEXT,
			latitude REAL NOT NULL,
			longitude REAL NOT NULL,
			altitude REAL,
			speed REAL,
			course REAL,
			source_time TEXT NOT NULL,
			received_at TEXT NOT NULL
		)`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	tx, err := database.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"source_port", "location_source", "precision_bits"} {
		exists, err := tableHasColumn(ctx, tx, "tak_positions", column)
		if err != nil || !exists {
			t.Fatalf("column %q exists=%v err=%v", column, exists, err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var applied bool
	if err := database.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = 2)",
	).Scan(&applied); err != nil || !applied {
		t.Fatalf("migration applied=%v err=%v", applied, err)
	}
	if err := database.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = 3)",
	).Scan(&applied); err != nil || !applied {
		t.Fatalf("node migration applied=%v err=%v", applied, err)
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
