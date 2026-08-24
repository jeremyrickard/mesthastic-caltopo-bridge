package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	pb "buf.build/gen/go/meshtastic/protobufs/protocolbuffers/go/meshtastic"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/model"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/tak"
	"google.golang.org/protobuf/proto"
)

type Archiver interface {
	Archive(context.Context, model.Packet, *model.Position, bool) (int64, int64, bool, error)
}

type NodeStore interface {
	SaveNode(context.Context, model.Node) error
	Node(context.Context, uint32) (model.Node, bool, error)
}

type Service struct {
	Store          Archiver
	Nodes          NodeStore
	EnqueueCalTopo bool
	WakeDeliveries func()
	Logger         *slog.Logger

	mu               sync.Mutex
	lastEncryptedLog time.Time
}

func (s *Service) Handle(ctx context.Context, message *pb.FromRadio) error {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if metadata := message.GetMetadata(); metadata != nil {
		logger.Info("Meshtastic radio metadata", "firmware_version", metadata.GetFirmwareVersion())
		return nil
	}
	if nodeInfo := message.GetNodeInfo(); nodeInfo != nil {
		return s.saveNode(ctx, nodeInfo.GetNum(), nodeInfo.GetUser())
	}
	if device := message.GetConfig().GetDevice(); device != nil {
		role := device.GetRole()
		if role != pb.Config_DeviceConfig_TRACKER &&
			role != pb.Config_DeviceConfig_TAK &&
			role != pb.Config_DeviceConfig_TAK_TRACKER {
			logger.Warn("attached radio is not configured for a supported tracking role",
				"role", role.String(),
			)
		} else {
			logger.Info("verified Meshtastic tracking role", "role", role.String())
		}
		return nil
	}
	if configID := message.GetConfigCompleteId(); configID != 0 {
		logger.Info("Meshtastic configuration received", "config_id", configID)
		return nil
	}
	packet := message.GetPacket()
	if packet == nil {
		return nil
	}
	record, position := tak.Decode(packet, time.Now().UTC())
	if node, found, err := nodeFromPacket(packet); err != nil {
		record.ParseStatus = "malformed"
		record.ParseError = err.Error()
	} else if found {
		if err := s.saveNode(ctx, node.Number, &pb.User{
			Id: node.ID, LongName: node.LongName, ShortName: node.ShortName,
		}); err != nil {
			return err
		}
	}
	if position != nil && position.Callsign == position.SourceID() && s.Nodes != nil {
		node, found, err := s.Nodes.Node(ctx, position.SourceNode)
		if err != nil {
			return err
		}
		if found {
			position.Callsign = node.LongName
			position.DeviceCallsign = node.ShortName
		}
	}
	packetID, positionID, inserted, err := s.Store.Archive(ctx, record, position, s.EnqueueCalTopo)
	if err != nil {
		return err
	}
	if record.Encrypted {
		s.logEncrypted(logger, record)
	}
	if position != nil && inserted {
		logger.Info("archived Meshtastic position",
			"packet_row_id", packetID,
			"position_row_id", positionID,
			"source_id", position.SourceID(),
			"callsign", position.Callsign,
			"latitude", position.Latitude,
			"longitude", position.Longitude,
		)
		if s.WakeDeliveries != nil {
			s.WakeDeliveries()
		}
	}
	return nil
}

func (s *Service) saveNode(ctx context.Context, number uint32, user *pb.User) error {
	if s.Nodes == nil || user == nil {
		return nil
	}
	return s.Nodes.SaveNode(ctx, model.Node{
		Number:    number,
		ID:        strings.TrimSpace(user.GetId()),
		LongName:  strings.TrimSpace(user.GetLongName()),
		ShortName: strings.TrimSpace(user.GetShortName()),
	})
}

func nodeFromPacket(packet *pb.MeshPacket) (model.Node, bool, error) {
	data := packet.GetDecoded()
	if data == nil || data.GetPortnum() != pb.PortNum_NODEINFO_APP {
		return model.Node{}, false, nil
	}
	var user pb.User
	if err := proto.Unmarshal(data.GetPayload(), &user); err != nil {
		return model.Node{}, false, fmt.Errorf("decode Meshtastic node info: %w", err)
	}
	return model.Node{
		Number:    packet.GetFrom(),
		ID:        strings.TrimSpace(user.GetId()),
		LongName:  strings.TrimSpace(user.GetLongName()),
		ShortName: strings.TrimSpace(user.GetShortName()),
	}, true, nil
}

func (s *Service) logEncrypted(logger *slog.Logger, packet model.Packet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if now.Sub(s.lastEncryptedLog) < time.Minute {
		return
	}
	s.lastEncryptedLog = now
	logger.Warn("radio forwarded an encrypted packet; verify its channel or PKI keys",
		"source", model.NodeID(packet.From),
		"packet_id", packet.MeshPacketID,
	)
}
