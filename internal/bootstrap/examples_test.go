package bootstrap

import (
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/adapters/driving/cli"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// TestExampleFoldersReadTheirLogs loads each example folder like
// `huginn --config <folder>` and draws a real line of its kind: the
// documentation examples must keep working.
func TestExampleFoldersReadTheirLogs(t *testing.T) {
	cases := []struct {
		folder, repo, container, line, want string
		hide                                []string
	}{
		{
			folder: "config", repo: "payment-service", container: "payment-service",
			line: `{"@timestamp":"2026-09-26T17:12:40.104Z","level":"WARN","thread_name":"http-nio-8080-exec-7","logger_name":"io.gimle.payment.gateway.GatewayClient","message":"latency high","kubernetes":{"pod_name":"p"}}`,
			want: "17:12:40.104  WARN [nio-8080-exec-7] i.g.p.gateway.GatewayClient    : latency high",
		},
		{
			folder: "config-node", repo: "orders", container: "orders",
			line: `{"level":40,"time":1790431703123,"pid":1,"hostname":"orders-6f7c9","name":"orders","req":{"id":"req-42","method":"POST"},"msg":"stock low"}`,
			want: "14:08:23.123 WARN  orders       POST | stock low", hide: []string{"trace"},
		},
		{
			folder: "config-nginx", repo: "storefront", container: "nginx",
			line: `10.0.0.7 - - [26/Sep/2026:19:12:40 +0200] "GET /cart HTTP/1.1" 503 512 "-" "Mozilla/5.0"`,
			want: "17:12:40.000 503    512 GET /cart HTTP/1.1", hide: []string{"client"},
		},
		{
			folder: "config-nginx", repo: "storefront", container: "api",
			line: `{"ts":"2026-09-26T17:12:40Z","severity":"error","msg":"payment declined"}`,
			want: "17:12:40.000 ERROR - : payment declined",
		},
	}
	for _, tc := range cases {
		t.Run(tc.folder+"/"+tc.container, func(t *testing.T) {
			c, err := LoadConfig(cli.Options{ConfigPath: "../../examples/" + tc.folder}, noFiles())
			if err != nil {
				t.Fatal(err)
			}
			lp, probs := logParts(c)
			if probs != nil {
				t.Fatal(probs)
			}
			e := lp.decoders.For(tc.repo, tc.container).Decode(domain.RawLine{Container: tc.container, Text: tc.line, Time: time.Unix(0, 0)})
			lo, ok := lp.layouts[e.Format]
			if !ok {
				t.Fatalf("no layout for format %q", e.Format)
			}
			var b strings.Builder
			for _, s := range lo.Render(e, ports.RenderOptions{Location: time.UTC, Hide: ports.ColumnSet{}.With(tc.hide...)}) {
				b.WriteString(s.Text)
			}
			if got := b.String(); got != tc.want {
				t.Errorf("\ngot  %q\nwant %q", got, tc.want)
			}
		})
	}
}
