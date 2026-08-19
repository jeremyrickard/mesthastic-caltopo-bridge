package ingest

import (
	"context"
	"log/slog"
	"sync"
	"time"

	pb "buf.build/gen/go/meshtastic/protobufs/protocolbuffers/go/meshtastic"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/model"
	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/tak"
)

type Archiver interface {
	Archive(context.Context, model.Packet, *model.Position, bool) (int64, int64, bool, error)
}

type Service struct {
	Store          Archiver
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
	if device := message.GetConfig().GetDevice(); device != nil {
		role := device.GetRole()
		if role != pb.Config_DeviceConfig_TAK && role != pb.Config_DeviceConfig_TAK_TRACKER {
			logger.Warn("attached radio is not configured for a TAK role; port-72 decompression may be unavailable",
				"role", role.String(),
			)
		} else {
			logger.Info("verified Meshtastic TAK role", "role", role.String())
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
	packetID, positionID, inserted, err := s.Store.Archive(ctx, record, position, s.EnqueueCalTopo)
	if err != nil {
		return err
	}
	if record.Encrypted {
		s.logEncrypted(logger, record)
	}
	if position != nil && inserted {
		logger.Info("archived TAK position",
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
