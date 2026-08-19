package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/jeremyrickard/mesthastic-caltopo-bridge/internal/model"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type Delivery struct {
	ID       int64
	Position model.Position
	Attempts int
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path != ":memory:" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve database path: %w", err)
		}
		path = absolute
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.initialize(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) initialize(ctx context.Context) error {
	for _, statement := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
		schema,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize SQLite database: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Archive(ctx context.Context, packet model.Packet, position *model.Position, enqueue bool) (int64, int64, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, false, fmt.Errorf("begin packet archive: %w", err)
	}
	defer tx.Rollback()

	rawPacket := packet.RawPacket
	if rawPacket == nil {
		rawPacket = []byte{}
	}
	rawPayload := packet.RawPayload
	if rawPayload == nil {
		rawPayload = []byte{}
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO mesh_packets (
			source_node, destination_node, mesh_packet_id, channel_id, port_num,
			hop_limit, hop_start, rssi, snr, via_mqtt, pki_encrypted, encrypted,
			received_at, radio_rx_time, raw_packet, raw_payload, parse_status, parse_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		packet.From, packet.To, packet.MeshPacketID, packet.Channel, nullablePort(packet),
		packet.HopLimit, packet.HopStart, packet.RSSI, packet.SNR, packet.ViaMQTT,
		packet.PKIEncrypted, packet.Encrypted, formatTime(packet.ReceivedAt),
		nullableTime(packet.RadioRxTime),
		rawPacket, rawPayload, packet.ParseStatus, nullableString(packet.ParseError),
	)
	if err != nil {
		return 0, 0, false, fmt.Errorf("insert mesh packet: %w", err)
	}
	packetID, err := result.LastInsertId()
	if err != nil {
		return 0, 0, false, fmt.Errorf("read mesh packet ID: %w", err)
	}
	if position == nil {
		if err := tx.Commit(); err != nil {
			return 0, 0, false, fmt.Errorf("commit packet archive: %w", err)
		}
		return packetID, 0, false, nil
	}

	dedupeKey := positionKey(packet, *position)
	result, err = tx.ExecContext(ctx, `
		INSERT INTO tak_positions (
			packet_id, dedupe_key, source_node, mesh_packet_id, callsign,
			device_callsign, latitude, longitude, altitude, speed, course,
			source_time, received_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(dedupe_key) DO NOTHING`,
		packetID, dedupeKey, position.SourceNode, position.MeshPacketID,
		position.Callsign, nullableString(position.DeviceCallsign),
		position.Latitude, position.Longitude, position.Altitude, position.Speed,
		position.Course, formatTime(position.SourceTime), formatTime(position.ReceivedAt),
	)
	if err != nil {
		return 0, 0, false, fmt.Errorf("insert TAK position: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, 0, false, fmt.Errorf("read TAK position insertion result: %w", err)
	}
	if rows == 0 {
		if err := tx.Commit(); err != nil {
			return 0, 0, false, fmt.Errorf("commit duplicate packet archive: %w", err)
		}
		return packetID, 0, false, nil
	}
	positionID, err := result.LastInsertId()
	if err != nil {
		return 0, 0, false, fmt.Errorf("read TAK position ID: %w", err)
	}
	if enqueue {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO caltopo_deliveries (position_id, state, attempts, next_attempt_at)
			VALUES (?, 'pending', 0, ?)`,
			positionID, formatTime(time.Now().UTC()),
		); err != nil {
			return 0, 0, false, fmt.Errorf("enqueue CalTopo delivery: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, false, fmt.Errorf("commit packet and position archive: %w", err)
	}
	return packetID, positionID, true, nil
}

func (s *Store) PendingDeliveries(ctx context.Context, limit int) ([]Delivery, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.attempts, p.id, p.source_node, p.mesh_packet_id,
		       p.callsign, COALESCE(p.device_callsign, ''), p.latitude, p.longitude,
		       p.altitude, p.speed, p.course, p.source_time, p.received_at
		FROM caltopo_deliveries d
		JOIN tak_positions p ON p.id = d.position_id
		WHERE d.state = 'pending' AND d.next_attempt_at <= ?
		  AND NOT EXISTS (
			SELECT 1
			FROM caltopo_deliveries earlier_d
			JOIN tak_positions earlier_p ON earlier_p.id = earlier_d.position_id
			WHERE earlier_d.state = 'pending'
			  AND earlier_p.source_node = p.source_node
			  AND (
				earlier_p.source_time < p.source_time OR
				(earlier_p.source_time = p.source_time AND earlier_d.id < d.id)
			  )
		  )
		ORDER BY p.source_time, d.id
		LIMIT ?`, formatTime(time.Now().UTC()), limit)
	if err != nil {
		return nil, fmt.Errorf("query pending CalTopo deliveries: %w", err)
	}
	defer rows.Close()
	var deliveries []Delivery
	for rows.Next() {
		var delivery Delivery
		var altitude, speed, course sql.NullFloat64
		var sourceTime, receivedAt string
		if err := rows.Scan(
			&delivery.ID, &delivery.Attempts, &delivery.Position.PacketID,
			&delivery.Position.SourceNode, &delivery.Position.MeshPacketID,
			&delivery.Position.Callsign, &delivery.Position.DeviceCallsign,
			&delivery.Position.Latitude, &delivery.Position.Longitude,
			&altitude, &speed, &course, &sourceTime, &receivedAt,
		); err != nil {
			return nil, fmt.Errorf("scan pending CalTopo delivery: %w", err)
		}
		delivery.Position.Altitude = floatPointer(altitude)
		delivery.Position.Speed = floatPointer(speed)
		delivery.Position.Course = floatPointer(course)
		delivery.Position.SourceTime, err = parseTime(sourceTime)
		if err != nil {
			return nil, err
		}
		delivery.Position.ReceivedAt, err = parseTime(receivedAt)
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending CalTopo deliveries: %w", err)
	}
	return deliveries, nil
}

func (s *Store) Positions(ctx context.Context) ([]model.Position, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.source_node, p.mesh_packet_id, p.callsign,
		       COALESCE(p.device_callsign, ''), p.latitude, p.longitude,
		       p.altitude, p.speed, p.course, p.source_time, p.received_at
		FROM tak_positions p
		WHERE NOT EXISTS (
			SELECT 1
			FROM tak_positions newer
			WHERE newer.source_node = p.source_node
			  AND (
				newer.source_time > p.source_time OR
				(newer.source_time = p.source_time AND newer.id > p.id)
			  )
		)
		ORDER BY p.source_time, p.id`)
	if err != nil {
		return nil, fmt.Errorf("query TAK positions: %w", err)
	}
	defer rows.Close()

	positions := make([]model.Position, 0)
	for rows.Next() {
		var position model.Position
		var altitude, speed, course sql.NullFloat64
		var sourceTime, receivedAt string
		if err := rows.Scan(
			&position.PacketID, &position.SourceNode, &position.MeshPacketID,
			&position.Callsign, &position.DeviceCallsign,
			&position.Latitude, &position.Longitude,
			&altitude, &speed, &course, &sourceTime, &receivedAt,
		); err != nil {
			return nil, fmt.Errorf("scan TAK position: %w", err)
		}
		position.Altitude = floatPointer(altitude)
		position.Speed = floatPointer(speed)
		position.Course = floatPointer(course)
		position.SourceTime, err = parseTime(sourceTime)
		if err != nil {
			return nil, err
		}
		position.ReceivedAt, err = parseTime(receivedAt)
		if err != nil {
			return nil, err
		}
		positions = append(positions, position)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate TAK positions: %w", err)
	}
	return positions, nil
}

func (s *Store) MarkDelivered(ctx context.Context, deliveryID int64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE caltopo_deliveries
		SET state = 'delivered', delivered_at = ?, last_error = NULL
		WHERE id = ? AND state = 'pending'`, formatTime(time.Now().UTC()), deliveryID)
	if err != nil {
		return fmt.Errorf("mark CalTopo delivery complete: %w", err)
	}
	return requireChanged(result, "pending CalTopo delivery")
}

func (s *Store) MarkFailed(ctx context.Context, deliveryID int64, attempts int, next time.Time, terminal bool, deliveryErr error) error {
	state := "pending"
	if terminal {
		state = "failed"
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE caltopo_deliveries
		SET state = ?, attempts = ?, next_attempt_at = ?, last_error = ?
		WHERE id = ? AND state = 'pending'`,
		state, attempts, formatTime(next), deliveryErr.Error(), deliveryID,
	)
	if err != nil {
		return fmt.Errorf("record CalTopo delivery failure: %w", err)
	}
	return requireChanged(result, "pending CalTopo delivery")
}

func (s *Store) TrackID(ctx context.Context, sourceID string) (string, bool, error) {
	var trackID string
	err := s.db.QueryRowContext(ctx,
		"SELECT track_id FROM caltopo_tracks WHERE source_id = ?", sourceID,
	).Scan(&trackID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("query CalTopo track mapping: %w", err)
	}
	return trackID, true, nil
}

func (s *Store) SaveTrack(ctx context.Context, sourceID, deviceID, trackID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO caltopo_tracks (source_id, device_id, track_id, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(source_id) DO UPDATE SET
			device_id = excluded.device_id,
			track_id = excluded.track_id`,
		sourceID, deviceID, trackID, formatTime(time.Now().UTC()),
	)
	if err != nil {
		return fmt.Errorf("save CalTopo track mapping: %w", err)
	}
	return nil
}

func positionKey(packet model.Packet, position model.Position) string {
	const dedupeWindow = 15 * time.Minute
	bucket := packet.ReceivedAt.Unix() / int64(dedupeWindow/time.Second)
	if packet.RadioRxTime != nil {
		bucket = packet.RadioRxTime.Unix() / int64(dedupeWindow/time.Second)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf(
		"%08x:%08x:%d:%.7f:%.7f:%x", packet.From, packet.MeshPacketID,
		bucket, position.Latitude, position.Longitude, packet.RawPayload,
	)))
	return fmt.Sprintf("%x", sum[:])
}

func nullablePort(packet model.Packet) any {
	if packet.Encrypted {
		return nil
	}
	return packet.Port
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func floatPointer(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored timestamp %q: %w", value, err)
	}
	return parsed, nil
}

func requireChanged(result sql.Result, description string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("%s was not found", description)
	}
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS mesh_packets (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	source_node INTEGER NOT NULL,
	destination_node INTEGER NOT NULL,
	mesh_packet_id INTEGER NOT NULL,
	channel_id INTEGER NOT NULL,
	port_num INTEGER,
	hop_limit INTEGER NOT NULL,
	hop_start INTEGER NOT NULL,
	rssi INTEGER,
	snr REAL NOT NULL,
	via_mqtt INTEGER NOT NULL,
	pki_encrypted INTEGER NOT NULL,
	encrypted INTEGER NOT NULL,
	received_at TEXT NOT NULL,
	radio_rx_time TEXT,
	raw_packet BLOB NOT NULL,
	raw_payload BLOB NOT NULL,
	parse_status TEXT NOT NULL,
	parse_error TEXT
);
CREATE INDEX IF NOT EXISTS mesh_packets_received_at_idx ON mesh_packets(received_at);
CREATE INDEX IF NOT EXISTS mesh_packets_source_idx ON mesh_packets(source_node, mesh_packet_id);

CREATE TABLE IF NOT EXISTS tak_positions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	packet_id INTEGER NOT NULL REFERENCES mesh_packets(id),
	dedupe_key TEXT NOT NULL UNIQUE,
	source_node INTEGER NOT NULL,
	mesh_packet_id INTEGER NOT NULL,
	callsign TEXT NOT NULL,
	device_callsign TEXT,
	latitude REAL NOT NULL CHECK(latitude BETWEEN -90 AND 90),
	longitude REAL NOT NULL CHECK(longitude BETWEEN -180 AND 180),
	altitude REAL,
	speed REAL,
	course REAL,
	source_time TEXT NOT NULL,
	received_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS tak_positions_source_time_idx ON tak_positions(source_node, source_time);

CREATE TABLE IF NOT EXISTS caltopo_tracks (
	source_id TEXT PRIMARY KEY,
	device_id TEXT NOT NULL UNIQUE,
	track_id TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS caltopo_deliveries (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	position_id INTEGER NOT NULL UNIQUE REFERENCES tak_positions(id),
	state TEXT NOT NULL CHECK(state IN ('pending', 'delivered', 'failed')),
	attempts INTEGER NOT NULL,
	next_attempt_at TEXT NOT NULL,
	last_error TEXT,
	delivered_at TEXT
);
CREATE INDEX IF NOT EXISTS caltopo_deliveries_pending_idx
	ON caltopo_deliveries(state, next_attempt_at);

INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`
