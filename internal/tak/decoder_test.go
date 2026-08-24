package tak

import (
	"math"
	"testing"
	"time"

	pb "buf.build/gen/go/meshtastic/protobufs/protocolbuffers/go/meshtastic"
	"google.golang.org/protobuf/proto"
)

func TestDecodeLegacyPosition(t *testing.T) {
	payload, err := proto.Marshal(&pb.TAKPacket{
		Contact: &pb.Contact{Callsign: "Rescue 1", DeviceCallsign: "tracker"},
		PayloadVariant: &pb.TAKPacket_Pli{Pli: &pb.PLI{
			LatitudeI:  398765432,
			LongitudeI: -1041234567,
			Altitude:   1734,
			Speed:      12,
			Course:     271,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rxTime := uint32(1_700_000_000)
	packet := decodedPacket(pb.PortNum_ATAK_PLUGIN, payload)
	packet.RxTime = &rxTime
	record, position := Decode(packet, time.Unix(1_700_000_100, 0))
	if record.ParseStatus != "position" || position == nil {
		t.Fatalf("status=%q error=%q position=%v", record.ParseStatus, record.ParseError, position)
	}
	if math.Abs(position.Latitude-39.8765432) > 1e-9 || math.Abs(position.Longitude+104.1234567) > 1e-9 {
		t.Fatalf("unexpected coordinates: %.7f, %.7f", position.Latitude, position.Longitude)
	}
	if position.Callsign != "Rescue 1" || position.Altitude == nil || *position.Altitude != 1734 {
		t.Fatalf("unexpected position: %+v", position)
	}
	if !position.SourceTime.Equal(time.Unix(int64(rxTime), 0)) {
		t.Fatalf("source time=%v", position.SourceTime)
	}
}

func TestDecodeClassifiesTraffic(t *testing.T) {
	tests := []struct {
		name   string
		packet *pb.MeshPacket
		status string
	}{
		{
			name: "encrypted",
			packet: &pb.MeshPacket{
				From: 1, PayloadVariant: &pb.MeshPacket_Encrypted{Encrypted: []byte{1, 2}},
			},
			status: "encrypted",
		},
		{
			name:   "v2",
			packet: decodedPacket(pb.PortNum_ATAK_PLUGIN_V2, []byte{0xff}),
			status: "unsupported_atak_v2",
		},
		{
			name:   "forwarder",
			packet: decodedPacket(pb.PortNum_ATAK_FORWARDER, []byte("data")),
			status: "unsupported_atak_forwarder",
		},
		{
			name:   "standard position",
			packet: decodedPacket(pb.PortNum_POSITION_APP, []byte{0xff}),
			status: "non_tak",
		},
		{
			name:   "malformed",
			packet: decodedPacket(pb.PortNum_ATAK_PLUGIN, []byte{0xff}),
			status: "malformed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record, position := Decode(test.packet, time.Now())
			if record.ParseStatus != test.status || position != nil {
				t.Fatalf("status=%q position=%v", record.ParseStatus, position)
			}
		})
	}
}

func TestDecodeCompressedLegacyPacketIsNotPublished(t *testing.T) {
	payload, err := proto.Marshal(&pb.TAKPacket{
		IsCompressed: true,
		PayloadVariant: &pb.TAKPacket_Pli{Pli: &pb.PLI{
			LatitudeI: 100, LongitudeI: 200,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	record, position := Decode(decodedPacket(pb.PortNum_ATAK_PLUGIN, payload), time.Now())
	if record.ParseStatus != "tak_compressed" || position != nil {
		t.Fatalf("status=%q position=%v", record.ParseStatus, position)
	}
}

func decodedPacket(port pb.PortNum, payload []byte) *pb.MeshPacket {
	return &pb.MeshPacket{
		From: 0x1234, Id: 99,
		PayloadVariant: &pb.MeshPacket_Decoded{Decoded: &pb.Data{
			Portnum: port, Payload: payload,
		}},
	}
}
