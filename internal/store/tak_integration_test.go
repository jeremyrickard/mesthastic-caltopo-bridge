package store_test

import (
	"context"
	"encoding/hex"
	"math"
	"path/filepath"
	"testing"
	"time"

	pb "buf.build/gen/go/meshtastic/protobufs/protocolbuffers/go/meshtastic"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/store"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/tak"
	"google.golang.org/protobuf/proto"
)

func TestArchivePositionAppPacket(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	meshPosition := &pb.Position{
		Time:           1_700_000_000,
		LocationSource: pb.Position_LOC_EXTERNAL,
		PrecisionBits:  13,
	}
	meshPosition.SetLatitudeI(398765432)
	meshPosition.SetLongitudeI(-1041234567)
	meshPosition.SetAltitude(1734)
	payload, err := proto.Marshal(meshPosition)
	if err != nil {
		t.Fatal(err)
	}
	packet := &pb.MeshPacket{
		From: 1, Id: 10,
		PayloadVariant: &pb.MeshPacket_Decoded{Decoded: &pb.Data{
			Portnum: pb.PortNum_POSITION_APP,
			Payload: payload,
		}},
	}
	record, position := tak.Decode(packet, time.Unix(1_700_000_100, 0))
	if _, _, inserted, err := database.Archive(ctx, record, position, false); err != nil || !inserted {
		t.Fatalf("inserted=%v err=%v", inserted, err)
	}
	positions, err := database.Positions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 1 ||
		math.Abs(positions[0].Latitude-39.8765432) > 1e-9 ||
		math.Abs(positions[0].Longitude+104.1234567) > 1e-9 ||
		positions[0].Altitude == nil || *positions[0].Altitude != 1734 ||
		positions[0].LocationSource != "LOC_EXTERNAL" ||
		positions[0].PrecisionBits != 13 {
		t.Fatalf("positions=%+v", positions)
	}
}

func TestArchiveCompressedTAKPositions(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	payloads := []string{
		"080112120A078403090F25A2E712078403090F25A2E71A040801100A220208622A100DB6F4331715DADF97C1188010289027",
		"080112120A078403090F25A2E312078403090F25A2E31A040801100A220208572A0D0DB6EA33171578D897C1188910",
	}
	for i, encoded := range payloads {
		payload, err := hex.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		packet := &pb.MeshPacket{
			From: uint32(i + 1),
			Id:   uint32(i + 10),
			PayloadVariant: &pb.MeshPacket_Decoded{Decoded: &pb.Data{
				Portnum: pb.PortNum_ATAK_PLUGIN,
				Payload: payload,
			}},
		}
		record, position := tak.Decode(packet, time.Now())
		if record.ParseStatus != "tak_callsign_undecodable" || position == nil {
			t.Fatalf("packet %d: status=%q error=%q position=%v", i, record.ParseStatus, record.ParseError, position)
		}
		_, positionID, inserted, err := database.Archive(ctx, record, position, false)
		if err != nil || !inserted || positionID == 0 {
			t.Fatalf("packet %d: position_id=%d inserted=%v err=%v", i, positionID, inserted, err)
		}
	}

	positions, err := database.Positions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != len(payloads) {
		t.Fatalf("positions=%d, want %d", len(positions), len(payloads))
	}
}
