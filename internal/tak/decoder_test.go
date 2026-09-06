package tak

import (
	"encoding/hex"
	"math"
	"testing"
	"time"

	pb "buf.build/gen/go/meshtastic/protobufs/protocolbuffers/go/meshtastic"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/takproto"
	"google.golang.org/protobuf/proto"
)

func TestDecodeLegacyPosition(t *testing.T) {
	payload, err := proto.Marshal(&takproto.TAKPacket{
		Contact: &takproto.Contact{Callsign: []byte("Rescue 1"), DeviceCallsign: []byte("tracker")},
		PayloadVariant: &takproto.TAKPacket_Pli{Pli: &takproto.PLI{
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

func TestDecodePositionApp(t *testing.T) {
	for _, test := range []struct {
		name           string
		locationSource pb.Position_LocSource
	}{
		{"internal GPS", pb.Position_LOC_INTERNAL},
		{"external GPS", pb.Position_LOC_EXTERNAL},
	} {
		t.Run(test.name, func(t *testing.T) {
			meshPosition := &pb.Position{
				Time:           1_700_000_000,
				LocationSource: test.locationSource,
				PrecisionBits:  14,
			}
			meshPosition.SetLatitudeI(398765432)
			meshPosition.SetLongitudeI(-1041234567)
			meshPosition.SetAltitude(1734)
			payload, err := proto.Marshal(meshPosition)
			if err != nil {
				t.Fatal(err)
			}

			record, position := Decode(decodedPacket(pb.PortNum_POSITION_APP, payload), time.Unix(1_700_000_100, 0))
			if record.ParseStatus != "position" || position == nil {
				t.Fatalf("status=%q error=%q position=%v", record.ParseStatus, record.ParseError, position)
			}
			if math.Abs(position.Latitude-39.8765432) > 1e-9 ||
				math.Abs(position.Longitude+104.1234567) > 1e-9 ||
				position.Altitude == nil || *position.Altitude != 1734 {
				t.Fatalf("unexpected position: %+v", position)
			}
			if position.SourcePort != int32(pb.PortNum_POSITION_APP) ||
				position.LocationSource != test.locationSource.String() ||
				position.PrecisionBits != 14 {
				t.Fatalf("unexpected metadata: %+v", position)
			}
			if !position.SourceTime.Equal(time.Unix(1_700_000_000, 0)) {
				t.Fatalf("source time=%v", position.SourceTime)
			}
		})
	}
}

func TestDecodePositionAppWithoutFix(t *testing.T) {
	payload, err := proto.Marshal(&pb.Position{LocationSource: pb.Position_LOC_EXTERNAL})
	if err != nil {
		t.Fatal(err)
	}
	record, position := Decode(decodedPacket(pb.PortNum_POSITION_APP, payload), time.Now())
	if record.ParseStatus != "position_no_fix" || position != nil {
		t.Fatalf("status=%q error=%q position=%v", record.ParseStatus, record.ParseError, position)
	}
}

func TestDecodePositionAppCanBeDisabled(t *testing.T) {
	record, position := DecodeWithOptions(
		decodedPacket(pb.PortNum_POSITION_APP, []byte{0xff}),
		time.Now(),
		DecodeOptions{DecodePositionApp: false},
	)
	if record.ParseStatus != "position_app_disabled" || position != nil {
		t.Fatalf("status=%q error=%q position=%v", record.ParseStatus, record.ParseError, position)
	}
}

func TestDecodeCompressedFirmwarePositions(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		latitude  int32
		longitude int32
		altitude  int32
	}{
		{
			name:      "firmware capture one",
			payload:   "080112120A078403090F25A2E712078403090F25A2E71A040801100A220208622A100DB6F4331715DADF97C1188010289027",
			latitude:  389280950,
			longitude: -1047011366,
			altitude:  2048,
		},
		{
			name:      "firmware capture two",
			payload:   "080112120A078403090F25A2E312078403090F25A2E31A040801100A220208572A0D0DB6EA33171578D897C1188910",
			latitude:  389278390,
			longitude: -1047013256,
			altitude:  2057,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := hex.DecodeString(test.payload)
			if err != nil {
				t.Fatal(err)
			}
			var takPacket takproto.TAKPacket
			if err := proto.Unmarshal(payload, &takPacket); err != nil {
				t.Fatal(err)
			}
			pli := takPacket.GetPli()
			if !takPacket.GetIsCompressed() || pli == nil ||
				pli.GetLatitudeI() != test.latitude ||
				pli.GetLongitudeI() != test.longitude ||
				pli.GetAltitude() != test.altitude {
				t.Fatalf("compressed=%v pli=%+v", takPacket.GetIsCompressed(), pli)
			}

			record, position := Decode(decodedPacket(pb.PortNum_ATAK_PLUGIN, payload), time.Now())
			if record.ParseStatus != "tak_callsign_undecodable" || position == nil {
				t.Fatalf("status=%q error=%q position=%+v", record.ParseStatus, record.ParseError, position)
			}
			if position.Callsign != "!00001234" {
				t.Fatalf("callsign=%q", position.Callsign)
			}
		})
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
			status: "malformed",
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

func TestDecodeEmptyLegacyPacketIsNonPosition(t *testing.T) {
	record, position := Decode(decodedPacket(pb.PortNum_ATAK_PLUGIN, nil), time.Now())
	if record.ParseStatus != "tak_non_position" || position != nil {
		t.Fatalf("status=%q error=%q position=%v", record.ParseStatus, record.ParseError, position)
	}
}

func TestDecodeInvalidUncompressedCallsignDoesNotRejectPosition(t *testing.T) {
	payload, err := proto.Marshal(&takproto.TAKPacket{
		Contact: &takproto.Contact{Callsign: []byte{0xff}},
		PayloadVariant: &takproto.TAKPacket_Pli{Pli: &takproto.PLI{
			LatitudeI: 100, LongitudeI: 200,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	record, position := Decode(decodedPacket(pb.PortNum_ATAK_PLUGIN, payload), time.Now())
	if record.ParseStatus != "tak_callsign_undecodable" || position == nil {
		t.Fatalf("status=%q error=%q position=%v", record.ParseStatus, record.ParseError, position)
	}
	if position.Callsign != "!00001234" {
		t.Fatalf("callsign=%q", position.Callsign)
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
