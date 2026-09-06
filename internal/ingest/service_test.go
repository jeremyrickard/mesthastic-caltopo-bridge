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

func TestHandleAssociatesPositionWithNodeShortName(t *testing.T) {
	nodes := &memoryNodes{callsigns: make(map[uint32]string)}
	archive := &memoryArchiver{}
	service := Service{
		Store:             archive,
		Nodes:             nodes,
		DecodePositionApp: true,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	nodeInfo := &pb.FromRadio{}
	nodeInfo.SetNodeInfo(&pb.NodeInfo{
		Num:  0x12f47fb2,
		User: &pb.User{ShortName: "R12", LongName: "Rescue Twelve"},
	})
	if err := service.Handle(context.Background(), nodeInfo); err != nil {
		t.Fatal(err)
	}

	meshPosition := &pb.Position{}
	meshPosition.SetLatitudeI(389280323)
	meshPosition.SetLongitudeI(-1047009533)
	payload, err := proto.Marshal(meshPosition)
	if err != nil {
		t.Fatal(err)
	}
	meshPacket := &pb.MeshPacket{
		From: 0x12f47fb2, Id: 2,
		PayloadVariant: &pb.MeshPacket_Decoded{Decoded: &pb.Data{
			Portnum: pb.PortNum_POSITION_APP, Payload: payload,
		}},
	}
	message := &pb.FromRadio{}
	message.SetPacket(meshPacket)
	if err := service.Handle(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if archive.position == nil || archive.position.Callsign != "R12" {
		t.Fatalf("position=%+v", archive.position)
	}
}

func TestHandleLearnsNodeInfoAppPacket(t *testing.T) {
	nodes := &memoryNodes{callsigns: make(map[uint32]string)}
	archive := &memoryArchiver{}
	service := Service{
		Store:  archive,
		Nodes:  nodes,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	payload, err := proto.Marshal(&pb.User{ShortName: "R12", LongName: "Rescue Twelve"})
	if err != nil {
		t.Fatal(err)
	}
	meshPacket := &pb.MeshPacket{
		From: 0x12f47fb2, Id: 1,
		PayloadVariant: &pb.MeshPacket_Decoded{Decoded: &pb.Data{
			Portnum: pb.PortNum_NODEINFO_APP, Payload: payload,
		}},
	}
	message := &pb.FromRadio{}
	message.SetPacket(meshPacket)
	if err := service.Handle(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if nodes.callsigns[0x12f47fb2] != "R12" {
		t.Fatalf("callsigns=%v", nodes.callsigns)
	}
	if archive.packet.ParseStatus != "non_tak" {
		t.Fatalf("packet=%+v", archive.packet)
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

type memoryNodes struct {
	callsigns map[uint32]string
}

func (n *memoryNodes) UpsertNode(_ context.Context, node uint32, shortName, longName string) error {
	if shortName != "" {
		n.callsigns[node] = shortName
	} else {
		n.callsigns[node] = longName
	}
	return nil
}

func (n *memoryNodes) NodeCallsign(_ context.Context, node uint32) (string, error) {
	return n.callsigns[node], nil
}
