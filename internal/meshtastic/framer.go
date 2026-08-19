package meshtastic

import (
	"encoding/binary"
	"fmt"
)

const (
	frameStart1 = 0x94
	frameStart2 = 0xc3
	maxFrame    = 512
)

type Framer struct {
	buffer []byte
}

func (f *Framer) Push(data []byte) ([][]byte, []error) {
	f.buffer = append(f.buffer, data...)
	var frames [][]byte
	var errs []error
	for {
		start := findStart(f.buffer)
		if start < 0 {
			if len(f.buffer) > 0 && f.buffer[len(f.buffer)-1] == frameStart1 {
				f.buffer = f.buffer[len(f.buffer)-1:]
			} else {
				f.buffer = f.buffer[:0]
			}
			return frames, errs
		}
		if start > 0 {
			f.buffer = f.buffer[start:]
		}
		if len(f.buffer) < 4 {
			return frames, errs
		}
		size := int(binary.BigEndian.Uint16(f.buffer[2:4]))
		if size == 0 || size > maxFrame {
			errs = append(errs, fmt.Errorf("invalid Meshtastic frame length %d", size))
			f.buffer = f.buffer[1:]
			continue
		}
		if len(f.buffer) < 4+size {
			return frames, errs
		}
		frame := make([]byte, size)
		copy(frame, f.buffer[4:4+size])
		frames = append(frames, frame)
		f.buffer = f.buffer[4+size:]
	}
}

func EncodeFrame(payload []byte) ([]byte, error) {
	if len(payload) == 0 || len(payload) > maxFrame {
		return nil, fmt.Errorf("invalid Meshtastic frame length %d", len(payload))
	}
	frame := make([]byte, 4+len(payload))
	frame[0], frame[1] = frameStart1, frameStart2
	binary.BigEndian.PutUint16(frame[2:4], uint16(len(payload)))
	copy(frame[4:], payload)
	return frame, nil
}

func findStart(data []byte) int {
	for i := 0; i+1 < len(data); i++ {
		if data[i] == frameStart1 && data[i+1] == frameStart2 {
			return i
		}
	}
	return -1
}
