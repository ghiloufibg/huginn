package kafka

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/sasl/scram"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

const (
	topic    = "orders.requested"
	user     = "reader"
	password = "s3cret-pw"
)

// recorder parses the request frames a reader writes on its TCP
// connections (plaintext clusters only), to prove what reached the wire.
type recorder struct {
	mu   sync.Mutex
	keys map[int16]int
}

func (r *recorder) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	return &recordingConn{Conn: c, r: r}, nil
}

func (r *recorder) forbidden() []int16 {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []int16
	for k := range r.keys {
		if _, ok := allowedKeys[k]; !ok {
			out = append(out, k)
		}
	}
	return out
}

type recordingConn struct {
	net.Conn
	r      *recorder
	buf    []byte
	remain int
}

func (c *recordingConn) Write(p []byte) (int, error) {
	data := p
	for len(data) > 0 {
		if c.remain > 0 {
			n := min(c.remain, len(data))
			c.remain -= n
			data = data[n:]
			continue
		}
		need := 6 - len(c.buf)
		n := min(need, len(data))
		c.buf = append(c.buf, data[:n]...)
		data = data[n:]
		if len(c.buf) == 6 {
			size := int(binary.BigEndian.Uint32(c.buf[:4]))
			key := int16(binary.BigEndian.Uint16(c.buf[4:6]))
			c.r.mu.Lock()
			c.r.keys[key]++
			c.r.mu.Unlock()
			c.remain, c.buf = size-2, c.buf[:0]
		}
	}
	return c.Conn.Write(p)
}

// newCluster starts an in-memory cluster with SASL (SCRAM-SHA-512) and a
// three-partition topic of 30 records written in the last minute.
func newCluster(t *testing.T, opts ...kfake.Opt) (*kfake.Cluster, func(n int)) {
	t.Helper()
	opts = append([]kfake.Opt{
		kfake.NumBrokers(2), kfake.SeedTopics(3, topic), kfake.EnableSASL(),
		kfake.Superuser("SCRAM-SHA-512", user, password),
	}, opts...)
	c, err := kfake.NewCluster(opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, func(n int) { produce(t, c, n) }
}

func produce(t *testing.T, c *kfake.Cluster, n int) {
	t.Helper()
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(c.ListenAddrs()...),
		kgo.SASL(scram.Auth{User: user, Pass: password}.AsSha512Mechanism()),
		kgo.RecordPartitioner(kgo.ManualPartitioner()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	now := time.Now()
	for i := range n {
		r := &kgo.Record{
			Topic: topic, Partition: int32(i % 3), Key: fmt.Appendf(nil, "k-%d", i),
			Value: fmt.Appendf(nil, `{"n":%d}`, i), Timestamp: now.Add(-time.Duration(n-i) * time.Second),
			Headers: []kgo.RecordHeader{{Key: "traceId", Value: []byte("t1")}},
		}
		if err := cl.ProduceSync(context.Background(), r).FirstErr(); err != nil {
			t.Fatal(err)
		}
	}
}

func conn(c *kfake.Cluster) domain.KafkaConnection {
	return domain.KafkaConnection{
		Bootstrap: c.ListenAddrs(), Security: "sasl_plaintext", Mechanism: "scram-sha-512",
		Username: domain.NewSecret(user), Password: domain.NewSecret(password),
	}
}

func fastOptions(r *recorder) Options {
	o := Options{ConnectTimeout: 3 * time.Second, RequestTimeout: 5 * time.Second, IdleEnd: 500 * time.Millisecond}
	if r != nil {
		o.DialContext = r.dial
	}
	return o
}

func TestContractAgainstKfake(t *testing.T) {
	portstest.RunTopicSourceContract(t, func(t *testing.T) portstest.TopicFixture {
		c, produceN := newCluster(t)
		produceN(30)
		rec := &recorder{keys: map[int16]int{}}
		t.Cleanup(func() {
			if bad := rec.forbidden(); len(bad) > 0 {
				t.Errorf("requests that are not reads reached the broker: %v", bad)
			}
			rec.mu.Lock()
			defer rec.mu.Unlock()
			if rec.keys[1] == 0 && rec.keys[3] == 0 {
				t.Error("the recorder saw no request: it does not observe the reader")
			}
		})
		return portstest.TopicFixture{
			Factory: &Factory{Options: fastOptions(rec)}, Conn: conn(c), Topic: topic, Tail: 4,
			Produce: func(t *testing.T) { produce(t, c, 3) },
		}
	})
}

func TestNoGroupNoCommitEverReachesTheBroker(t *testing.T) {
	c, produceN := newCluster(t)
	produceN(30)
	var mu sync.Mutex
	seen := map[int16]int{}
	autoCreate := false
	reading := true
	c.Control(func(req kmsg.Request) (kmsg.Response, error, bool) {
		mu.Lock()
		defer mu.Unlock()
		if reading {
			seen[req.Key()]++
			if m, ok := req.(*kmsg.MetadataRequest); ok && m.AllowAutoTopicCreation {
				autoCreate = true
			}
		}
		return nil, nil, false
	})
	src, err := (&Factory{Options: fastOptions(nil)}).Open(context.Background(), conn(c))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := src.Read(ctx, ports.TopicRead{Topic: topic, Since: time.Hour, Limit: 100, Follow: true, ReadCommitted: true})
	if err != nil {
		t.Fatal(err)
	}
	got := 0
	for b := range ch {
		got += len(b.Records)
		if b.HistoryDone {
			break
		}
	}
	cancel()
	for range ch { //nolint:revive // drain
	}
	if got != 30 {
		t.Fatalf("read %d records, want 30", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for k := range seen {
		if _, ok := allowedKeys[k]; !ok {
			t.Errorf("broker received request %d (%s)", k, kmsg.NameForKey(k))
		}
	}
	for _, k := range []kmsg.Key{kmsg.JoinGroup, kmsg.SyncGroup, kmsg.Heartbeat, kmsg.OffsetCommit, kmsg.FindCoordinator, kmsg.Produce, kmsg.CreateTopics, kmsg.InitProducerID} {
		if seen[int16(k)] > 0 {
			t.Errorf("%s sent", kmsg.NameForKey(int16(k)))
		}
	}
	if autoCreate {
		t.Error("a metadata request allowed topic creation")
	}
}

func TestGuardRefusesAWriteAndStopsTheSource(t *testing.T) {
	c, _ := newCluster(t)
	var produced bool
	var mu sync.Mutex
	c.ControlKey(int16(kmsg.Produce), func(kmsg.Request) (kmsg.Response, error, bool) {
		mu.Lock()
		produced = true
		mu.Unlock()
		return nil, nil, false
	})
	s, err := (&Factory{Options: fastOptions(nil)}).Open(context.Background(), conn(c))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	src := s.(*source)
	req := kmsg.NewPtrProduceRequest() // what a bug would send
	req.Acks, req.TimeoutMillis = 1, 1000
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = src.admin.Request(ctx, req)
	if err == nil {
		t.Fatal("the produce request went through")
	}
	if src.violation.Err() == nil || !strings.Contains(src.violation.Err().Error(), "refused Kafka request 0") {
		t.Fatalf("violation not recorded: %v", src.violation.Err())
	}
	if _, err := src.Read(context.Background(), ports.TopicRead{Topic: topic, Tail: 1}); err == nil || !strings.Contains(err.Error(), "read only") {
		t.Fatalf("a source that refused a write stops reading: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if produced {
		t.Fatal("the broker received the produce request")
	}
}

func TestAuthenticationAndNetworkFailures(t *testing.T) {
	c, _ := newCluster(t)
	bad := conn(c)
	bad.Password = domain.NewSecret("wrong")
	_, err := (&Factory{Options: fastOptions(nil)}).Open(context.Background(), bad)
	if !errors.Is(err, domain.ErrUnauthorized) || strings.Contains(err.Error(), "wrong") || strings.Contains(err.Error(), password) {
		t.Fatalf("wrong password: %v", err)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close() // nothing listens there now
	none := conn(c)
	none.Bootstrap = []string{addr}
	o := fastOptions(nil)
	o.ConnectTimeout, o.RequestTimeout = time.Second, time.Second
	_, err = (&Factory{Options: o}).Open(context.Background(), none)
	if !errors.Is(err, domain.ErrUnreachable) || !strings.Contains(err.Error(), "check the network or VPN") {
		t.Fatalf("unreachable: %v", err)
	}
}

func selfSigned(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "kfake"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, der
}

func TestTLSWithTruststore(t *testing.T) {
	cert, der := selfSigned(t)
	c, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, topic), kfake.EnableSASL(),
		kfake.Superuser("SCRAM-SHA-512", user, password), kfake.TLS(&tls.Config{Certificates: []tls.Certificate{cert}}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cn := conn(c)
	cn.Security, cn.CACerts = "sasl_ssl", [][]byte{der}
	src, err := (&Factory{Options: fastOptions(nil)}).Open(context.Background(), cn)
	if err != nil {
		t.Fatalf("trusted CA: %v", err)
	}
	infos, err := src.Describe(context.Background(), []string{topic})
	src.Close()
	if err != nil || infos[0].Partitions != 1 {
		t.Fatalf("describe over TLS: %+v %v", infos, err)
	}
	_, other := selfSigned(t)
	cn.CACerts = [][]byte{other}
	_, err = (&Factory{Options: fastOptions(nil)}).Open(context.Background(), cn)
	if !errors.Is(err, domain.ErrUnreachable) || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("untrusted CA: %v", err)
	}
}

func TestEmptyTopicEndsAtOnce(t *testing.T) {
	c, _ := newCluster(t)
	src, err := (&Factory{Options: fastOptions(nil)}).Open(context.Background(), conn(c))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	ch, err := src.Read(context.Background(), ports.TopicRead{Topic: topic, Tail: 10, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	b, ok := <-ch
	if !ok || !b.HistoryDone || len(b.Records) != 0 {
		t.Fatalf("empty topic: %+v %v", b, ok)
	}
	if _, ok := <-ch; ok {
		t.Fatal("not closed")
	}
}

func TestRecordsCarryKeysHeadersAndTime(t *testing.T) {
	c, produceN := newCluster(t)
	produceN(3)
	src, err := (&Factory{Options: fastOptions(nil)}).Open(context.Background(), conn(c))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	ch, _ := src.Read(context.Background(), ports.TopicRead{Topic: topic, Tail: 5, Limit: 5})
	var recs []domain.KafkaRecord
	for b := range ch {
		recs = append(recs, b.Records...)
	}
	if len(recs) != 3 {
		t.Fatalf("%d records", len(recs))
	}
	r := recs[0]
	if !strings.HasPrefix(string(r.Key), "k-") || r.ValueSize != len(r.Value) || r.Time.IsZero() || len(r.Headers) != 1 || r.Headers[0].Key != "traceId" {
		t.Fatalf("record: %+v", r)
	}
}
