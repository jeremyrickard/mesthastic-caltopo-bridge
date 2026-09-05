package meshtastic

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"testing"

	pb "buf.build/gen/go/meshtastic/protobufs/protocolbuffers/go/meshtastic"
	"google.golang.org/protobuf/proto"
)

func TestFramerHandlesNoiseAndFragmentation(t *testing.T) {
	first, err := EncodeFrame([]byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncodeFrame([]byte{4, 5})
	if err != nil {
		t.Fatal(err)
	}
	var framer Framer
	frames, errs := framer.Push(append([]byte("debug output"), first[:3]...))
	if len(frames) != 0 || len(errs) != 0 {
		t.Fatalf("partial frame produced frames=%d errors=%d", len(frames), len(errs))
	}
	frames, errs = framer.Push(append(first[3:], second...))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(frames) != 2 || !bytes.Equal(frames[0], []byte{1, 2, 3}) || !bytes.Equal(frames[1], []byte{4, 5}) {
		t.Fatalf("unexpected frames: %v", frames)
	}
}

func TestFramerRejectsOversizedFrameAndResynchronizes(t *testing.T) {
	valid, err := EncodeFrame([]byte{9})
	if err != nil {
		t.Fatal(err)
	}
	input := append([]byte{frameStart1, frameStart2, 0x02, 0x01}, valid...)
	var framer Framer
	frames, errs := framer.Push(input)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if len(frames) != 1 || !bytes.Equal(frames[0], []byte{9}) {
		t.Fatalf("framer did not resynchronize: %v", frames)
	}
}

func TestSourceConsumeWritesHandshakeAndReadsMessage(t *testing.T) {
	packet := &pb.FromRadio{}
	packet.SetPacket(&pb.MeshPacket{From: 42})
	encoded, err := proto.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := EncodeFrame(encoded)
	if err != nil {
		t.Fatal(err)
	}
	rw := &memoryPort{reader: bytes.NewReader(frame)}
	var received *pb.FromRadio
	source := Source{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handler: func(_ context.Context, message *pb.FromRadio) error {
			received = message
			return nil
		},
	}
	err = source.consume(context.Background(), rw, source.Logger)
	if err != io.EOF {
		t.Fatalf("consume error = %v, want EOF", err)
	}
	if received == nil || received.GetPacket().GetFrom() != 42 {
		t.Fatalf("unexpected received message: %v", received)
	}
	var framer Framer
	frames, errs := framer.Push(rw.written.Bytes())
	if len(errs) != 0 || len(frames) != 1 {
		t.Fatalf("invalid handshake framing: frames=%d errors=%v", len(frames), errs)
	}
	var request pb.ToRadio
	if err := proto.Unmarshal(frames[0], &request); err != nil {
		t.Fatal(err)
	}
	if !request.HasWantConfigId() {
		t.Fatal("handshake did not request configuration")
	}
}

func TestSourceDebugLogsSerialDataAndDecodedMessage(t *testing.T) {
	packet := &pb.FromRadio{}
	packet.SetPacket(&pb.MeshPacket{From: 42})
	encoded, err := proto.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := EncodeFrame(encoded)
	if err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	source := Source{
		Debug:  true,
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		Handler: func(_ context.Context, _ *pb.FromRadio) error {
			return nil
		},
	}
	err = source.consume(context.Background(), &memoryPort{reader: bytes.NewReader(frame)}, source.Logger)
	if err != io.EOF {
		t.Fatalf("consume error = %v, want EOF", err)
	}
	output := logs.String()
	if !bytes.Contains([]byte(output), []byte("radio debug: received serial data")) ||
		!bytes.Contains([]byte(output), []byte("data_hex="+hex.EncodeToString(frame))) {
		t.Fatalf("raw serial data was not logged: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("radio debug: decoded FromRadio message")) ||
		!bytes.Contains([]byte(output), []byte("from:  42")) {
		t.Fatalf("decoded message was not logged: %s", output)
	}
}

type memoryPort struct {
	reader  *bytes.Reader
	written bytes.Buffer
}

func (p *memoryPort) Read(data []byte) (int, error)  { return p.reader.Read(data) }
func (p *memoryPort) Write(data []byte) (int, error) { return p.written.Write(data) }
