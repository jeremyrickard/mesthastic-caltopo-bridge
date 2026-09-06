package store_test

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	pb "buf.build/gen/go/meshtastic/protobufs/protocolbuffers/go/meshtastic"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/store"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/tak"
)

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
