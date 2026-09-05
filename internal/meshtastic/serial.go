package meshtastic

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"time"

	pb "buf.build/gen/go/meshtastic/protobufs/protocolbuffers/go/meshtastic"
	"go.bug.st/serial"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

type Handler func(context.Context, *pb.FromRadio) error

type Opener func() (io.ReadWriteCloser, error)

type Source struct {
	Open    Opener
	Handler Handler
	Logger  *slog.Logger
	Debug   bool
}

func NewSerialSource(device string, baud int, debug bool, logger *slog.Logger, handler Handler) *Source {
	return &Source{
		Open: func() (io.ReadWriteCloser, error) {
			port, err := serial.Open(device, &serial.Mode{BaudRate: baud})
			if err != nil {
				return nil, err
			}
			if err := port.SetReadTimeout(time.Second); err != nil {
				port.Close()
				return nil, err
			}
			return port, nil
		},
		Handler: handler,
		Logger:  logger,
		Debug:   debug,
	}
}

func ListDevices() ([]string, error) {
	return serial.GetPortsList()
}

func (s *Source) Run(ctx context.Context) error {
	if s.Open == nil || s.Handler == nil {
		return errors.New("Meshtastic source requires an opener and handler")
	}
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return nil
		}
		port, err := s.Open()
		if err != nil {
			logger.Error("opening Meshtastic serial device", "error", err, "retry_in", backoff)
			if !sleep(ctx, jitter(backoff)) {
				return nil
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		logger.Info("connected to Meshtastic serial device")
		backoff = time.Second
		err = s.consume(ctx, port, logger)
		closeErr := port.Close()
		if ctx.Err() != nil {
			return nil
		}
		logger.Error("Meshtastic serial connection lost", "error", errors.Join(err, closeErr), "retry_in", backoff)
		if !sleep(ctx, jitter(backoff)) {
			return nil
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (s *Source) consume(ctx context.Context, port io.ReadWriter, logger *slog.Logger) error {
	request := &pb.ToRadio{}
	configID := rand.Uint32()
	if configID == 0 {
		configID = 1
	}
	request.SetWantConfigId(configID)
	payload, err := proto.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal configuration request: %w", err)
	}
	frame, err := EncodeFrame(payload)
	if err != nil {
		return err
	}
	if err := writeAll(port, frame); err != nil {
		return fmt.Errorf("write configuration request: %w", err)
	}

	var framer Framer
	buf := make([]byte, 1024)
	for {
		if ctx.Err() != nil {
			return nil
		}
		n, readErr := port.Read(buf)
		if n > 0 {
			if s.Debug {
				logger.Info("radio debug: received serial data",
					"byte_count", n,
					"data_hex", hex.EncodeToString(buf[:n]),
				)
			}
			frames, frameErrs := framer.Push(buf[:n])
			for _, frameErr := range frameErrs {
				logger.Warn("discarding invalid serial frame", "error", frameErr)
			}
			for _, encoded := range frames {
				var message pb.FromRadio
				if err := proto.Unmarshal(encoded, &message); err != nil {
					logger.Warn("discarding invalid FromRadio protobuf", "error", err)
					continue
				}
				if s.Debug {
					logger.Info("radio debug: decoded FromRadio message",
						"byte_count", len(encoded),
						"message", prototext.Format(&message),
					)
				}
				handlerBackoff := time.Second
				for {
					if err := s.Handler(ctx, &message); err != nil {
						if ctx.Err() != nil {
							return nil
						}
						logger.Error("archiving FromRadio message failed; pausing intake without dropping packet",
							"error", err,
							"retry_in", handlerBackoff,
						)
						if !sleep(ctx, handlerBackoff) {
							return nil
						}
						handlerBackoff = min(handlerBackoff*2, 30*time.Second)
						continue
					}
					break
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return io.EOF
			}
			return fmt.Errorf("read serial device: %w", readErr)
		}
	}
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func sleep(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func jitter(duration time.Duration) time.Duration {
	return duration/2 + time.Duration(rand.Int64N(int64(duration)))
}
