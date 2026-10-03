package kafka

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

// allowedKeys are the only Kafka requests Huginn may send (D-057): reading
// metadata and offsets, fetching records, authenticating. OffsetForLeaderEpoch
// (23) is read only too: the client asks it after a leader change to check
// its position. Everything that writes (Produce, OffsetCommit, group
// membership, topic creation, transactions, telemetry) is absent.
var allowedKeys = map[int16]string{
	1:  "Fetch",
	2:  "ListOffsets",
	3:  "Metadata",
	17: "SaslHandshake",
	18: "ApiVersions",
	23: "OffsetForLeaderEpoch",
	36: "SaslAuthenticate",
}

// Violation records the first request a guard refused, shared by every
// connection of a client.
type Violation struct {
	mu  sync.Mutex
	key int16
	set atomic.Bool
}

// Err returns the refusal, nil when nothing was refused.
func (v *Violation) Err() error {
	if !v.set.Load() {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return fmt.Errorf("internal error: refused Kafka request %d, which is not a read (Huginn is read only)", v.key)
}

func (v *Violation) record(key int16) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.set.Load() {
		v.key = key
		v.set.Store(true)
	}
}

// guardConn checks every request frame written to a broker connection,
// above TLS. The bytes of a frame's size and API key are held back until
// the key is known: an allowed request is then written whole, a refused
// one is never written at all and the connection is closed.
//
// A request frame is a 4-byte size followed by a request header starting
// with the 2-byte API key. Frames may be split across writes, or several
// may share one write; the guard tracks the frame boundaries.
type guardConn struct {
	net.Conn
	violation *Violation

	mu     sync.Mutex
	head   [6]byte // size and API key of the frame being started, held back
	nhead  int
	remain int // body bytes of the current frame still to come
	closed bool
}

func newGuard(c net.Conn, v *Violation) *guardConn { return &guardConn{Conn: c, violation: v} }

// Write implements net.Conn. It reports p as written once its allowed
// bytes are passed on (held header bytes count as written).
func (g *guardConn) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return 0, net.ErrClosed
	}
	out, ok := g.scan(p)
	if len(out) > 0 {
		if _, err := g.Conn.Write(out); err != nil {
			return 0, err
		}
	}
	if !ok {
		g.closed = true
		_ = g.Close()
		return 0, fmt.Errorf("%w: refused a request that is not a read", net.ErrClosed)
	}
	return len(p), nil
}

// scan returns the bytes of p that may be written now, and false when a
// refused request starts in p (the bytes before it are still returned).
func (g *guardConn) scan(p []byte) ([]byte, bool) {
	out := make([]byte, 0, len(p)+len(g.head))
	for len(p) > 0 {
		if g.remain > 0 {
			take := min(g.remain, len(p))
			out = append(out, p[:take]...)
			g.remain -= take
			p = p[take:]
			continue
		}
		take := copy(g.head[g.nhead:], p)
		g.nhead += take
		p = p[take:]
		if g.nhead < len(g.head) {
			break // the key comes with the next write
		}
		g.nhead = 0
		size := int32(binary.BigEndian.Uint32(g.head[:4]))
		key := int16(binary.BigEndian.Uint16(g.head[4:6]))
		if _, ok := allowedKeys[key]; !ok || size < 2 {
			g.violation.record(key)
			return out, false
		}
		out = append(out, g.head[:]...)
		g.remain = int(size) - 2
	}
	return out, true
}
