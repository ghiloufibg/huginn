//go:build ignore

// m22-apiproxy: a recording, fault-injecting reverse proxy between a client
// (Huginn) and a Kubernetes API server, for the M22 QA (per-verb API count,
// network cut, Metrics API outage) on a cluster where audit logs are not
// available. It is a QA tool, not part of Huginn: it is built on its own
//
//	go run ./deploy/gke-qa/tools/m22-apiproxy.go -upstream https://IP -upstream-ca ca.pem -dir DIR
//
// and prints the CA of its own certificate to DIR/proxy-ca.pem for the client's
// kubeconfig (server: https://127.0.0.1:PORT). It forwards the client's
// Authorization header untouched, so the client authenticates as usual.
//
// DIR/requests.tsv gets one line per request: epoch_ms, method, verb (get,
// list, watch, create, ...), path with object names replaced, status, bytes.
// Fault flags, read on every request (touch to turn on, remove to turn off):
//
//	DIR/cut         drop every connection and refuse new ones (a network cut)
//	DIR/metrics503  answer 503 to /apis/metrics.k8s.io/ (metrics-server gone)
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type counter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (c *counter) WriteHeader(s int) { c.status = s; c.ResponseWriter.WriteHeader(s) }
func (c *counter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = 200
	}
	n, err := c.ResponseWriter.Write(b)
	c.bytes += int64(n)
	return n, err
}
func (c *counter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

var nameSeg = regexp.MustCompile(`(/namespaces/)[^/]+`)

// shape keeps the resource path and drops the names: /namespaces/x/pods/y/log
// becomes /namespaces/*/pods/*/log.
func shape(p string) string {
	p = nameSeg.ReplaceAllString(p, "${1}*")
	parts := strings.Split(p, "/")
	// /api/v1/namespaces/*/pods/NAME[/sub] and /apis/G/V/namespaces/*/R/NAME[/sub]
	for i, s := range parts {
		if s == "pods" || s == "deployments" || s == "replicasets" || s == "services" || s == "jobs" || s == "cronjobs" || s == "daemonsets" || s == "statefulsets" {
			if i+1 < len(parts) && parts[i+1] != "" {
				parts[i+1] = "*"
			}
		}
	}
	return strings.Join(parts, "/")
}

func verb(r *http.Request) string {
	q := r.URL.Query()
	switch r.Method {
	case http.MethodGet:
		if q.Get("watch") == "true" || q.Get("watch") == "1" {
			return "watch"
		}
		last := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if strings.HasSuffix(r.URL.Path, "/log") {
			return "log"
		}
		switch last {
		case "pods", "deployments", "replicasets", "services", "jobs", "cronjobs", "daemonsets", "statefulsets", "namespaces", "nodes", "events", "configmaps":
			return "list"
		}
		if strings.Contains(r.URL.Path, "metrics.k8s.io") && (last == "pods" || last == "nodes") {
			return "list"
		}
		return "get"
	case http.MethodPost:
		return "create"
	case http.MethodPut:
		return "update"
	case http.MethodPatch:
		return "patch"
	case http.MethodDelete:
		return "delete"
	}
	return strings.ToLower(r.Method)
}

func selfSigned(dir string) tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "m22-apiproxy"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		log.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(dir, "proxy-ca.pem"), pemBytes, 0o644); err != nil {
		log.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func main() {
	upstream := flag.String("upstream", "", "API server URL, https://IP")
	caFile := flag.String("upstream-ca", "", "PEM CA of the API server")
	dir := flag.String("dir", ".", "directory for requests.tsv, proxy-ca.pem and the flag files")
	addr := flag.String("listen", "127.0.0.1:8443", "listen address")
	flag.Parse()
	u, err := url.Parse(*upstream)
	if err != nil || *upstream == "" {
		log.Fatal("-upstream is required")
	}
	pool := x509.NewCertPool()
	ca, err := os.ReadFile(*caFile)
	if err != nil || !pool.AppendCertsFromPEM(ca) {
		log.Fatalf("-upstream-ca: %v", err)
	}
	out, err := os.OpenFile(filepath.Join(*dir, "requests.tsv"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	var mu sync.Mutex
	rp := httputil.NewSingleHostReverseProxy(u)
	rp.FlushInterval = -1
	rp.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}, ForceAttemptHTTP2: true}
	flag1 := func(n string) bool { _, err := os.Stat(filepath.Join(*dir, n)); return err == nil }

	var cmu sync.Mutex
	conns := map[net.Conn]struct{}{}
	srv := &http.Server{
		Addr: *addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := &counter{ResponseWriter: w}
			start := time.Now()
			switch {
			case flag1("cut"):
				panic(http.ErrAbortHandler)
			case flag1("metrics503") && strings.HasPrefix(r.URL.Path, "/apis/metrics.k8s.io"):
				http.Error(c, "m22-apiproxy: metrics API unavailable", http.StatusServiceUnavailable)
			default:
				rp.ServeHTTP(c, r)
			}
			mu.Lock()
			fmt.Fprintf(out, "%d\t%s\t%s\t%s\t%d\t%d\t%d\n", start.UnixMilli(), r.Method, verb(r), shape(r.URL.Path), c.status, c.bytes, time.Since(start).Milliseconds())
			mu.Unlock()
		}),
		ConnState: func(c net.Conn, s http.ConnState) {
			cmu.Lock()
			defer cmu.Unlock()
			switch s {
			case http.StateNew:
				conns[c] = struct{}{}
			case http.StateClosed, http.StateHijacked:
				delete(conns, c)
			}
		},
	}
	// The cut: every 200 ms, while DIR/cut exists, close every connection.
	go func() {
		for range time.Tick(200 * time.Millisecond) {
			if !flag1("cut") {
				continue
			}
			cmu.Lock()
			for c := range conns {
				_ = c.Close()
			}
			cmu.Unlock()
		}
	}()
	ln, err := tls.Listen("tcp", *addr, &tls.Config{Certificates: []tls.Certificate{selfSigned(*dir)}, NextProtos: []string{"h2", "http/1.1"}})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("m22-apiproxy %s -> %s, files in %s", *addr, *upstream, *dir)
	log.Fatal(srv.Serve(ln))
}
