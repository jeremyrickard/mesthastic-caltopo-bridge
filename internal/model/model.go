package model

import "time"

type Packet struct {
	From         uint32
	To           uint32
	MeshPacketID uint32
	Channel      uint32
	Port         int32
	HopLimit     uint32
	HopStart     uint32
	RSSI         *int32
	SNR          float32
	ViaMQTT      bool
	PKIEncrypted bool
	Encrypted    bool
	ReceivedAt   time.Time
	RadioRxTime  *time.Time
	RawPacket    []byte
	RawPayload   []byte
	ParseStatus  string
	ParseError   string
}

type Position struct {
	PacketID       int64
	SourceNode     uint32
	MeshPacketID   uint32
	Callsign       string
	DeviceCallsign string
	Latitude       float64
	Longitude      float64
	Altitude       *float64
	Speed          *float64
	Course         *float64
	SourceTime     time.Time
	ReceivedAt     time.Time
}

type Node struct {
	Number    uint32
	ID        string
	LongName  string
	ShortName string
}

func (p Position) SourceID() string {
	return NodeID(p.SourceNode)
}

func NodeID(node uint32) string {
	const hex = "0123456789abcdef"
	buf := [9]byte{'!'}
	for i := 8; i > 0; i-- {
		buf[i] = hex[node&0xf]
		node >>= 4
	}
	return string(buf[:])
}
