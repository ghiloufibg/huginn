package demo

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

// generator produces the deterministic content of one container's lines.
type generator struct {
	seed  int64
	spec  podSpec
	pod   string
	start time.Time // world start, for the error burst
}

func (g generator) rng(k int64) *rand.Rand {
	return rand.New(rand.NewPCG(uint64(g.seed), hashOf(g.pod, k)))
}

// burst reports whether t falls in this pod's error burst: payment-service
// has one from 10 to 4 minutes before the session started.
func (g generator) burst(t time.Time) bool {
	return g.spec.repo.name == "payment-service" && t.After(g.start.Add(-10*time.Minute)) && t.Before(g.start.Add(-4*time.Minute))
}

// mdcRepos are the repositories whose messages carry a Logback MDC context
// written into the text, "key=value… - message - key=value…", most values
// empty, the way some logging stacks do; examples/config reads them with a
// transform.
var mdcRepos = map[string]bool{"order-orchestrator": true}

// at returns the application entry for slot k at time t.
func (g generator) at(k int64, t time.Time) entry {
	e := g.plainAt(k, t)
	if mdcRepos[g.spec.repo.name] {
		e.message = g.withMDC(k, e)
	}
	return e
}

// withMDC wraps the message of e in its MDC context. Entries of a request
// (with a trace id) fill part of it; the others leave every key empty.
func (g generator) withMDC(k int64, e entry) string {
	var route, method, corr, req, status, result string
	if e.trace != "" {
		r := rand.New(rand.NewPCG(uint64(g.seed), hashOf(g.pod, "mdc", k)))
		route, method = "/v1/"+noun(g.spec.repo.name)+"s", []string{"GET", "POST", "PUT"}[r.IntN(3)]
		corr, req = e.trace[:16], fmt.Sprintf("%08x", r.Uint32())
		status, result = "200", "OK"
		if e.level == "ERROR" {
			status, result = "500", "KO"
		}
		// Agree with the message when it names the request.
		for _, m := range []string{"GET", "POST", "PUT"} {
			if strings.Contains(e.message, " "+m+" /") {
				method = m
			}
		}
		if _, after, ok := strings.Cut(e.message, " status="); ok && len(after) >= 3 {
			status = after[:3]
		}
		// A slow downstream call ends the request with 503, while the line
		// is only a warning: level_from shows it as an error.
		if strings.HasPrefix(e.message, "downstream latency") {
			status, result = "503", "KO"
		}
	}
	return fmt.Sprintf("route=%s method=%s correlation-id=%s business_id= - %s - user_id= x-forwarded-for= request_id=%s http_status=%s result=%s status_code= error_code= activity_id= activity_name= process_instance_id=",
		route, method, corr, e.message, req, status, result)
}

// plainAt returns the application entry for slot k at time t, without
// context in its message.
func (g generator) plainAt(k int64, t time.Time) entry {
	r := g.rng(k)
	pkg, cls := g.spec.repo.pkg, domainClass(g.spec.workload)
	thread := fmt.Sprintf("http-nio-8080-exec-%d", 1+r.IntN(10))
	trace := fmt.Sprintf("%016x%016x", r.Uint64(), r.Uint64())
	if mdcRepos[g.spec.repo.name] {
		// One request is served by several pods: neighbouring slots of
		// every pod share a trace id, for the trace view (v).
		trace = fmt.Sprintf("%016x%016x", hashOf(g.spec.repo.name, "trace", k/2), hashOf(g.spec.repo.name, "span", k/2))
	}
	path := "/v1/" + noun(g.spec.repo.name) + "s"
	roll := r.IntN(100)
	errPct := 3
	switch {
	case g.burst(t):
		errPct = 35
	case g.spec.cond == degraded:
		errPct = 20
	}
	switch {
	case roll < errPct:
		return g.errorEntry(r, t, thread, trace)
	case roll < errPct+10:
		svc := []string{"adyen-gateway", "token-vault", "redis-master", "ledger-writer"}[r.IntN(4)]
		return entry{
			at: t, level: "WARN", logger: pkg + ".client.DownstreamClient", thread: thread, trace: trace,
			message: fmt.Sprintf("downstream latency above threshold service=%s p95=%dms traceId=%s", svc, 700+r.IntN(600), trace[:8]),
		}
	case roll < errPct+30:
		switch r.IntN(3) {
		case 0:
			return entry{
				at: t, level: "DEBUG", logger: "com.zaxxer.hikari.pool.HikariPool", thread: "HikariPool-1 housekeeper",
				message: fmt.Sprintf("HikariPool-1 - Pool stats (total=40, active=%d, idle=%d, waiting=0)", 5+r.IntN(20), 10+r.IntN(20)),
			}
		case 1:
			return entry{
				at: t, level: "DEBUG", logger: pkg + ".cache.IdempotencyCache", thread: thread, trace: trace,
				message: fmt.Sprintf("cache hit key=%s_%05x ttl=%ds", noun(g.spec.repo.name)[:3], r.IntN(1<<20), 30+r.IntN(270)),
			}
		default:
			return entry{
				at: t, level: "DEBUG", logger: pkg + ".web.TraceFilter", thread: thread, trace: trace,
				message: fmt.Sprintf("trace sampled traceId=%s spanId=%s", trace[:8], trace[16:22]),
			}
		}
	case roll < errPct+36:
		return entry{
			at: t, level: "INFO", logger: pkg + ".jobs.ScheduledJobs", thread: "scheduling-1",
			message: fmt.Sprintf("scheduled job %s-sync finished items=%d duration=%dms", noun(g.spec.repo.name), r.IntN(400), 20+r.IntN(900)),
		}
	default:
		method, status := "GET", 200
		if r.IntN(3) == 0 {
			method, status = "POST", 201
		}
		return entry{
			at: t, level: "INFO", logger: pkg + ".web." + cls + "Controller", thread: thread, trace: trace,
			message: fmt.Sprintf("request completed %s %s status=%d duration=%dms traceId=%s", method, path, status, 5+r.IntN(120), trace[:8]),
		}
	}
}

func (g generator) errorEntry(r *rand.Rand, t time.Time, thread, trace string) entry {
	pkg, cls := g.spec.repo.pkg, domainClass(g.spec.workload)
	order := fmt.Sprintf("ord_%06x", r.IntN(1<<24))
	switch r.IntN(3) {
	case 0:
		return entry{
			at: t, level: "ERROR", logger: pkg + ".service." + cls + "Service", thread: thread, trace: trace,
			extra:   &extraFields{OrderID: order, Customer: fmt.Sprintf("customer%d@example.com", r.IntN(900))},
			message: fmt.Sprintf("Request processing failed orderId=%s traceId=%s", order, trace[:8]),
			stack: pkg + ".client.UpstreamTimeoutException: upstream request timed out after 200ms\n" +
				"\tat " + pkg + ".client.DownstreamClient.call(DownstreamClient.java:184)\n" +
				"\tat " + pkg + ".service." + cls + "Service.process(" + cls + "Service.java:92)\n" +
				"\tat " + pkg + ".web." + cls + "Controller.create(" + cls + "Controller.java:57)\n" +
				"\tat org.springframework.web.servlet.FrameworkServlet.service(FrameworkServlet.java:885)\n" +
				"\tat org.apache.catalina.core.ApplicationFilterChain.doFilter(ApplicationFilterChain.java:138)\n" +
				"\t... 42 common frames omitted\n" +
				"Caused by: java.net.SocketTimeoutException: Read timed out\n" +
				"\tat java.base/sun.nio.ch.NioSocketImpl.timedFinishConnect(NioSocketImpl.java:546)\n" +
				"\t... 12 common frames omitted",
		}
	case 1:
		return entry{
			at: t, level: "ERROR", logger: pkg + ".web.GlobalExceptionHandler", thread: thread, trace: trace,
			message: fmt.Sprintf("Validation failed for request %s traceId=%s: field 'amount' must be positive", order, trace[:8]),
		}
	default:
		return entry{
			at: t, level: "ERROR", logger: "io.lettuce.core.RedisChannelHandler", thread: "lettuce-nioEventLoop-4-1",
			message: "Connection reset by peer; reconnect scheduled in 200ms",
			stack: "io.lettuce.core.RedisConnectionException: connection reset by peer\n" +
				"\tat io.lettuce.core.protocol.CommandHandler.exceptionCaught(CommandHandler.java:218)\n" +
				"\tat io.netty.channel.AbstractChannelHandlerContext.invokeExceptionCaught(AbstractChannelHandlerContext.java:346)",
		}
	}
}

// crashEntries are the last lines of an instance that died with reason.
func (g generator) crashEntries(reason string, t time.Time) []entry {
	pkg, cls := g.spec.repo.pkg, domainClass(g.spec.workload)
	switch reason {
	case "OOMKilled":
		return []entry{
			{at: t.Add(-20 * time.Second), level: "WARN", logger: pkg + ".jobs.ScheduledJobs", thread: "scheduling-1", message: "batch size 50000 exceeds recommended maximum 5000"},
			{
				at: t.Add(-3 * time.Second), level: "ERROR", logger: pkg + ".jobs.ScheduledJobs", thread: "scheduling-1", message: "Unexpected error in scheduled task",
				stack: "java.lang.OutOfMemoryError: Java heap space\n\tat java.base/java.util.Arrays.copyOf(Arrays.java:3541)\n\tat " + pkg + ".jobs.BatchLoader.load(BatchLoader.java:77)",
			},
		}
	default:
		return []entry{
			{
				at: t.Add(-2 * time.Second), level: "WARN", logger: "o.s.b.w.s.c.AnnotationConfigServletWebServerApplicationContext", thread: "main",
				message: "Exception encountered during context initialization - cancelling refresh attempt: org.springframework.beans.factory.BeanCreationException: Error creating bean with name 'searchClient'",
			},
			{
				at: t.Add(-time.Second), level: "ERROR", logger: "o.s.boot.SpringApplication", thread: "main", message: "Application run failed",
				stack: "org.springframework.beans.factory.BeanCreationException: Error creating bean with name 'searchClient' defined in class path resource [" + cls + "Config.class]\n" +
					"\tat org.springframework.beans.factory.support.AbstractAutowireCapableBeanFactory.initializeBean(AbstractAutowireCapableBeanFactory.java:1806)\n" +
					"Caused by: java.net.ConnectException: Connection refused (elasticsearch.search.svc:9200)\n" +
					"\tat java.base/sun.nio.ch.Net.pollConnect(Native Method)",
			},
		}
	}
}

// sidecar returns a line for an auxiliary container at slot k.
func (g generator) sidecar(container string, k int64, t time.Time) entry {
	r := g.rng(k)
	switch container {
	case "istio-proxy":
		return entry{at: t, plain: true, message: fmt.Sprintf(`[%s] "GET /v1/%ss HTTP/1.1" 200 - via_upstream - "-" 0 %d %d %d "-" "okhttp/4.12.0" "%016x" "%s:8080" "127.0.0.6:8080" inbound|8080|| - default`,
			t.UTC().Format("2006-01-02T15:04:05.000Z"), noun(g.spec.repo.name), 200+r.IntN(4000), 3+r.IntN(90), 2+r.IntN(80), r.Uint64(), g.spec.workload)}
	case "vault-agent":
		return entry{at: t, plain: true, message: t.UTC().Format("2006-01-02T15:04:05.000Z") + ` [INFO]  agent: (runner) rendered "(dynamic)" => "/vault/secrets/config.env"`}
	default:
		return entry{at: t, plain: true, message: "init: iptables rules applied"}
	}
}
