package tak

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	pb "buf.build/gen/go/meshtastic/protobufs/protocolbuffers/go/meshtastic"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/model"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/takproto"
	"google.golang.org/protobuf/proto"
)

var ErrNotPosition = errors.New("TAK packet is not a position report")

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
		position, callsignUndecodable, err := decodeLegacy(record, data.GetPayload())
		switch {
		case err == nil:
			if callsignUndecodable {
				record.ParseStatus = "tak_callsign_undecodable"
			} else {
				record.ParseStatus = "position"
			}
			return record, position
		case errors.Is(err, ErrNotPosition):
			record.ParseStatus = "tak_non_position"
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

func decodeLegacy(packet model.Packet, payload []byte) (*model.Position, bool, error) {
	var takPacket takproto.TAKPacket
	if err := proto.Unmarshal(payload, &takPacket); err != nil {
		return nil, false, fmt.Errorf("decode legacy TAK packet: %w", err)
	}
	pli := takPacket.GetPli()
	if pli == nil {
		return nil, false, ErrNotPosition
	}
	latitude := float64(pli.GetLatitudeI()) * 1e-7
	longitude := float64(pli.GetLongitudeI()) * 1e-7
	position, err := newPosition(packet, latitude, longitude)
	if err != nil {
		return nil, false, err
	}
	callsign, deviceCallsign, callsignUndecodable :=
		decodeCallsigns(takPacket.GetContact(), takPacket.GetIsCompressed())
	position.Callsign = callsign
	position.DeviceCallsign = deviceCallsign
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
	return position, callsignUndecodable, nil
}

func decodeCallsigns(contact *takproto.Contact, compressed bool) (string, string, bool) {
	if contact == nil {
		return "", "", false
	}
	callsign := contact.GetCallsign()
	deviceCallsign := contact.GetDeviceCallsign()
	if compressed {
		return "", "", len(callsign) > 0 || len(deviceCallsign) > 0
	}
	if !utf8.Valid(callsign) || !utf8.Valid(deviceCallsign) {
		return "", "", true
	}
	return strings.TrimSpace(string(callsign)), strings.TrimSpace(string(deviceCallsign)), false
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
