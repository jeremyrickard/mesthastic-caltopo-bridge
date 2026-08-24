package ingest

import (
	"context"
	"io"
	"log/slog"
	"testing"

	pb "buf.build/gen/go/meshtastic/protobufs/protocolbuffers/go/meshtastic"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/model"
	"google.golang.org/protobuf/proto"
)

func TestHandleArchivesAndWakesDeliveryWorker(t *testing.T) {
	payload, err := proto.Marshal(&pb.TAKPacket{
		Contact: &pb.Contact{Callsign: "Team 1"},
		PayloadVariant: &pb.TAKPacket_Pli{Pli: &pb.PLI{
			LatitudeI: 400000000, LongitudeI: -1050000000,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	meshPacket := &pb.MeshPacket{
		From: 1, Id: 2,
		PayloadVariant: &pb.MeshPacket_Decoded{Decoded: &pb.Data{
			Portnum: pb.PortNum_ATAK_PLUGIN, Payload: payload,
		}},
	}
	message := &pb.FromRadio{}
	message.SetPacket(meshPacket)
	archive := &memoryArchiver{}
	woke := false
	service := Service{
		Store:          archive,
		EnqueueCalTopo: true,
		WakeDeliveries: func() { woke = true },
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := service.Handle(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if archive.packet.ParseStatus != "position" || archive.position == nil {
		t.Fatalf("packet=%+v position=%+v", archive.packet, archive.position)
	}
	if !archive.enqueue || !woke {
		t.Fatalf("enqueue=%v woke=%v", archive.enqueue, woke)
	}
}

type memoryArchiver struct {
	packet   model.Packet
	position *model.Position
	enqueue  bool
}

func (a *memoryArchiver) Archive(_ context.Context, packet model.Packet, position *model.Position, enqueue bool) (int64, int64, bool, error) {
	a.packet, a.position, a.enqueue = packet, position, enqueue
	return 1, 2, true, nil
}
