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
	default:
		record.ParseStatus = "non_tak"
	}
	return record, nil
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
		SourceNode:     packet.From,
		MeshPacketID:   packet.MeshPacketID,
		Callsign:       strings.TrimSpace(takPacket.GetContact().GetCallsign()),
		DeviceCallsign: strings.TrimSpace(takPacket.GetContact().GetDeviceCallsign()),
		Latitude:       latitude,
		Longitude:      longitude,
		SourceTime:     sourceTime,
		ReceivedAt:     packet.ReceivedAt,
	}
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

func timeFromPacket(packet model.Packet) time.Time {
	if packet.RadioRxTime != nil {
		return *packet.RadioRxTime
	}
	return packet.ReceivedAt
}
