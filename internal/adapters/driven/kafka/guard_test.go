package kafka

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

type sink struct {
	net.Conn
	written bytes.Buffer
	closed  bool
}

func (s *sink) Write(p []byte) (int, error) { return s.written.Write(p) }
func (s *sink) Close() error                { s.closed = true; return nil }

// frame builds a request frame: size, API key, then body bytes.
func frame(key int16, body string) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(2+len(body)))
	b = binary.BigEndian.AppendUint16(b, uint16(key))
	return append(b, body...)
}

func TestGuardPassesReadsWhole(t *testing.T) {
	s := &sink{}
	g := newGuard(s, &Violation{})
	all := append(append(frame(18, "apiversions"), frame(3, "metadata")...), frame(1, "fetch-body")...)
	// One byte at a time, then everything at once: the same bytes arrive.
	for _, b := range all {
		if n, err := g.Write([]byte{b}); n != 1 || err != nil {
			t.Fatalf("write: %d %v", n, err)
		}
	}
	if n, err := g.Write(all); n != len(all) || err != nil {
		t.Fatal(err)
	}
	if want := append(append([]byte{}, all...), all...); !bytes.Equal(s.written.Bytes(), want) {
		t.Fatalf("written %q", s.written.Bytes())
	}
}

func TestGuardRefusesWritesBeforeAnyByte(t *testing.T) {
	for _, key := range []int16{0, 8, 10, 11, 12, 14, 19, 22, 24, 72} { // produce, commit, coordinator, join, heartbeat, sync, create, init producer, add partitions, telemetry
		s := &sink{}
		v := &Violation{}
		g := newGuard(s, v)
		ok := frame(3, "metadata")
		bad := frame(key, "payload")
		if _, err := g.Write(append(append([]byte{}, ok...), bad[:3]...)); err != nil {
			t.Fatal(err)
		}
		if _, err := g.Write(bad[3:]); err == nil {
			t.Fatalf("key %d accepted", key)
		}
		if !bytes.Equal(s.written.Bytes(), ok) || !s.closed || v.Err() == nil {
			t.Fatalf("key %d: written %q closed %v err %v", key, s.written.Bytes(), s.closed, v.Err())
		}
		if _, err := g.Write(frame(3, "x")); err == nil {
			t.Fatal("a closed guard writes nothing more")
		}
	}
}

func TestGuardRefusesMalformedSize(t *testing.T) {
	s := &sink{}
	g := newGuard(s, &Violation{})
	if _, err := g.Write([]byte{0, 0, 0, 1, 0, 3}); err == nil || s.written.Len() != 0 {
		t.Fatal("a frame too short for its key is refused")
	}
}

func FuzzGuard(f *testing.F) {
	f.Add(frame(3, "m"), 2)
	f.Add(append(frame(1, "f"), frame(0, "p")...), 5)
	f.Fuzz(func(t *testing.T, data []byte, split int) {
		s := &sink{}
		g := newGuard(s, &Violation{})
		if split < 0 || split > len(data) {
			split = len(data) / 2
		}
		_, _ = g.Write(data[:split])
		_, _ = g.Write(data[split:])
		// Whatever was written is a sequence of allowed, complete-or-
		// trailing frames: walk it and check every key.
		w := s.written.Bytes()
		for len(w) >= 6 {
			size := int(binary.BigEndian.Uint32(w[:4]))
			key := int16(binary.BigEndian.Uint16(w[4:6]))
			if _, ok := allowedKeys[key]; !ok {
				t.Fatalf("key %d written", key)
			}
			if size+4 > len(w) {
				break
			}
			w = w[size+4:]
		}
	})
}
