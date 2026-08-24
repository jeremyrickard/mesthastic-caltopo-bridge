package tak

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	pb "buf.build/gen/go/meshtastic/protobufs/protocolbuffers/go/meshtastic"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/model"
	"google.golang.org/protobuf/proto"
)

var (
	ErrNotPosition = errors.New("TAK packet is not a position report")
	ErrCompressed  = errors.New("compressed legacy TAK packet requires firmware decompression")
)

func Decode(packet *pb.MeshPacket, receivedAt time.Time) (model.Packet, *model.Position) {
	rawPacket, marshalErr := proto.Marshal(packet)
	record := model.Packet{
		From:         packet.GetFrom(),
		To:           packet.GetTo(),
		MeshPacketID: packet.GetId(),
		Channel:      packet.GetChannel(),
		HopLimit:     packet.GetHopLimit(),
		HopStart:     packet.GetHopStart(),
		SNR:          packet.GetRxSnr(),
		ViaMQTT:      packet.GetViaMqtt(),
		PKIEncrypted: packet.GetPkiEncrypted(),
		ReceivedAt:   receivedAt.UTC(),
		RawPacket:    rawPacket,
	}
	if packet.HasRxRssi() {
		value := packet.GetRxRssi()
		record.RSSI = &value
	}
	if packet.HasRxTime() {
		value := time.Unix(int64(packet.GetRxTime()), 0).UTC()
		record.RadioRxTime = &value
	}
	if marshalErr != nil {
		record.ParseStatus = "invalid_packet"
		record.ParseError = marshalErr.Error()
		return record, nil
	}

	data := packet.GetDecoded()
	if data == nil {
		record.Encrypted = true
		record.RawPayload = append([]byte(nil), packet.GetEncrypted()...)
		record.ParseStatus = "encrypted"
		return record, nil
	}
	record.Port = int32(data.GetPortnum())
	record.RawPayload = append([]byte(nil), data.GetPayload()...)
	switch data.GetPortnum() {
	case pb.PortNum_POSITION_APP:
		position, err := decodePosition(record, data.GetPayload())
		if err == nil {
			record.ParseStatus = "position"
			return record, position
		}
		record.ParseStatus = "malformed"
		record.ParseError = err.Error()
	case pb.PortNum_ATAK_PLUGIN:
		position, err := decodeLegacy(record, data.GetPayload())
		switch {
		case err == nil:
			record.ParseStatus = "position"
			return record, position
		case errors.Is(err, ErrNotPosition):
			record.ParseStatus = "tak_non_position"
		case errors.Is(err, ErrCompressed):
			record.ParseStatus = "tak_compressed"
		default:
			record.ParseStatus = "malformed"
			record.ParseError = err.Error()
		}
	case pb.PortNum_ATAK_PLUGIN_V2:
		record.ParseStatus = "unsupported_atak_v2"
	case pb.PortNum_ATAK_FORWARDER:
		record.ParseStatus = "unsupported_atak_forwarder"
	case pb.PortNum_NODEINFO_APP:
		record.ParseStatus = "node_info"
	default:
		record.ParseStatus = "non_tak"
	}
	return record, nil
}

func decodePosition(packet model.Packet, payload []byte) (*model.Position, error) {
	var meshPosition pb.Position
	if err := proto.Unmarshal(payload, &meshPosition); err != nil {
		return nil, fmt.Errorf("decode Meshtastic position: %w", err)
	}
	if !meshPosition.HasLatitudeI() || !meshPosition.HasLongitudeI() {
		return nil, errors.New("Meshtastic position is missing coordinates")
	}

	position, err := newPosition(
		packet,
		float64(meshPosition.GetLatitudeI())*1e-7,
		float64(meshPosition.GetLongitudeI())*1e-7,
	)
	if err != nil {
		return nil, err
	}
	if timestamp := meshPosition.GetTimestamp(); timestamp != 0 {
		position.SourceTime = time.Unix(
			int64(timestamp),
			int64(meshPosition.GetTimestampMillisAdjust())*int64(time.Millisecond),
		).UTC()
	} else if timestamp := meshPosition.GetTime(); timestamp != 0 {
		position.SourceTime = time.Unix(int64(timestamp), 0).UTC()
	}
	if meshPosition.HasAltitude() {
		value := float64(meshPosition.GetAltitude())
		position.Altitude = &value
	} else if meshPosition.HasAltitudeHae() {
		value := float64(meshPosition.GetAltitudeHae())
		position.Altitude = &value
	}
	if meshPosition.HasGroundSpeed() {
		value := float64(meshPosition.GetGroundSpeed())
		position.Speed = &value
	}
	if meshPosition.HasGroundTrack() {
		value := float64(meshPosition.GetGroundTrack()) / 100
		position.Course = &value
	}
	return position, nil
}

func decodeLegacy(packet model.Packet, payload []byte) (*model.Position, error) {
	var takPacket pb.TAKPacket
	if err := proto.Unmarshal(payload, &takPacket); err != nil {
		return nil, fmt.Errorf("decode legacy TAK packet: %w", err)
	}
	if takPacket.GetIsCompressed() {
		return nil, ErrCompressed
	}
	pli := takPacket.GetPli()
	if pli == nil {
		return nil, ErrNotPosition
	}
	latitude := float64(pli.GetLatitudeI()) * 1e-7
	longitude := float64(pli.GetLongitudeI()) * 1e-7
	position, err := newPosition(packet, latitude, longitude)
	if err != nil {
		return nil, err
	}
	position.Callsign = strings.TrimSpace(takPacket.GetContact().GetCallsign())
	position.DeviceCallsign = strings.TrimSpace(takPacket.GetContact().GetDeviceCallsign())
	if position.Callsign == "" {
		position.Callsign = model.NodeID(packet.From)
	}
	if altitude := pli.GetAltitude(); altitude != 0 {
		value := float64(altitude)
		position.Altitude = &value
	}
	if speed := pli.GetSpeed(); speed != 0 {
		value := float64(speed)
		position.Speed = &value
	}
	if course := pli.GetCourse(); course != 0 {
		value := float64(course)
		position.Course = &value
	}
	return position, nil
}

func newPosition(packet model.Packet, latitude, longitude float64) (*model.Position, error) {
	if math.IsNaN(latitude) || latitude < -90 || latitude > 90 {
		return nil, fmt.Errorf("invalid latitude %.7f", latitude)
	}
	if math.IsNaN(longitude) || longitude < -180 || longitude > 180 {
		return nil, fmt.Errorf("invalid longitude %.7f", longitude)
	}
	sourceTime := packet.ReceivedAt
	if packetTime := timeFromPacket(packet); !packetTime.IsZero() {
		sourceTime = packetTime
	}
	position := &model.Position{
		SourceNode:   packet.From,
		MeshPacketID: packet.MeshPacketID,
		Callsign:     model.NodeID(packet.From),
		Latitude:     latitude,
		Longitude:    longitude,
		SourceTime:   sourceTime,
		ReceivedAt:   packet.ReceivedAt,
	}
	return position, nil
}

func timeFromPacket(packet model.Packet) time.Time {
	if packet.RadioRxTime != nil {
		return *packet.RadioRxTime
	}
	return packet.ReceivedAt
}
