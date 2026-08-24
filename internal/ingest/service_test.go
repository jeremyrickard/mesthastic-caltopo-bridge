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

func TestHandleUsesNodeNameForTrackerPosition(t *testing.T) {
	ctx := context.Background()
	nodes := &memoryNodeStore{}
	service := Service{
		Store:  &memoryArchiver{},
		Nodes:  nodes,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	nodeInfo := &pb.FromRadio{}
	nodeInfo.SetNodeInfo(&pb.NodeInfo{
		Num: 0x73711445,
		User: &pb.User{
			Id: "!73711445", LongName: "Rescue Tracker", ShortName: "RT",
		},
	})
	if err := service.Handle(ctx, nodeInfo); err != nil {
		t.Fatal(err)
	}

	payload, err := proto.Marshal(&pb.Position{
		LatitudeI: proto.Int32(389283840), LongitudeI: proto.Int32(-1047265280),
	})
	if err != nil {
		t.Fatal(err)
	}
	message := &pb.FromRadio{}
	message.SetPacket(&pb.MeshPacket{
		From: 0x73711445, Id: 3,
		PayloadVariant: &pb.MeshPacket_Decoded{Decoded: &pb.Data{
			Portnum: pb.PortNum_POSITION_APP, Payload: payload,
		}},
	})
	if err := service.Handle(ctx, message); err != nil {
		t.Fatal(err)
	}
	position := service.Store.(*memoryArchiver).position
	if position == nil || position.Callsign != "Rescue Tracker" || position.DeviceCallsign != "RT" {
		t.Fatalf("position=%+v", position)
	}
}

func TestHandleStoresBroadcastNodeInfo(t *testing.T) {
	ctx := context.Background()
	nodes := &memoryNodeStore{}
	service := Service{
		Store:  &memoryArchiver{},
		Nodes:  nodes,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	payload, err := proto.Marshal(&pb.User{
		Id: "!0000002a", LongName: "Team 42", ShortName: "42",
	})
	if err != nil {
		t.Fatal(err)
	}
	message := &pb.FromRadio{}
	message.SetPacket(&pb.MeshPacket{
		From: 42,
		PayloadVariant: &pb.MeshPacket_Decoded{Decoded: &pb.Data{
			Portnum: pb.PortNum_NODEINFO_APP, Payload: payload,
		}},
	})
	if err := service.Handle(ctx, message); err != nil {
		t.Fatal(err)
	}
	if nodes.node.LongName != "Team 42" || nodes.node.Number != 42 {
		t.Fatalf("node=%+v", nodes.node)
	}
}

type memoryArchiver struct {
	packet   model.Packet
	position *model.Position
	enqueue  bool
}

type memoryNodeStore struct {
	node model.Node
}

func (s *memoryNodeStore) SaveNode(_ context.Context, node model.Node) error {
	s.node = node
	return nil
}

func (s *memoryNodeStore) Node(_ context.Context, number uint32) (model.Node, bool, error) {
	return s.node, s.node.Number == number, nil
}

func (a *memoryArchiver) Archive(_ context.Context, packet model.Packet, position *model.Position, enqueue bool) (int64, int64, bool, error) {
	a.packet, a.position, a.enqueue = packet, position, enqueue
	return 1, 2, true, nil
}
