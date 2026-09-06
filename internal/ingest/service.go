package ingest

import (
	"context"
	"log/slog"
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

type NodeDirectory interface {
	UpsertNode(context.Context, uint32, string, string) error
	NodeCallsign(context.Context, uint32) (string, error)
}

type Service struct {
	Store             Archiver
	Nodes             NodeDirectory
	EnqueueCalTopo    bool
	DecodePositionApp bool
	WakeDeliveries    func()
	Logger            *slog.Logger

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
	if device := message.GetConfig().GetDevice(); device != nil {
		logger.Info("Meshtastic radio role", "role", device.GetRole().String())
		return nil
	}
	if configID := message.GetConfigCompleteId(); configID != 0 {
		logger.Info("Meshtastic configuration received", "config_id", configID)
		return nil
	}
	if nodeInfo := message.GetNodeInfo(); nodeInfo != nil {
		return s.rememberNode(ctx, nodeInfo.GetNum(), nodeInfo.GetUser())
	}
	packet := message.GetPacket()
	if packet == nil {
		return nil
	}
	if data := packet.GetDecoded(); data != nil && data.GetPortnum() == pb.PortNum_NODEINFO_APP {
		var user pb.User
		if err := proto.Unmarshal(data.GetPayload(), &user); err != nil {
			logger.Warn("could not decode Meshtastic node info", "source", model.NodeID(packet.GetFrom()), "error", err)
		} else if err := s.rememberNode(ctx, packet.GetFrom(), &user); err != nil {
			return err
		}
	}
	record, position := tak.DecodeWithOptions(packet, time.Now().UTC(), tak.DecodeOptions{
		DecodePositionApp: s.DecodePositionApp,
	})
	if position != nil && position.Callsign == position.SourceID() && s.Nodes != nil {
		callsign, err := s.Nodes.NodeCallsign(ctx, position.SourceNode)
		if err != nil {
			return err
		}
		if callsign != "" {
			position.Callsign = callsign
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
		logger.Info("archived position",
			"packet_row_id", packetID,
			"position_row_id", positionID,
			"source", position.SourceID(),
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

func (s *Service) rememberNode(ctx context.Context, node uint32, user *pb.User) error {
	if s.Nodes == nil || user == nil {
		return nil
	}
	return s.Nodes.UpsertNode(ctx, node, user.GetShortName(), user.GetLongName())
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
