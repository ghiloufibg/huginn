package logformat

import (
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// logstash mirrors the built-in "logstash" profile of the configuration.
var logstash = Profile{
	Timestamp:    []string{"@timestamp", "timestamp", "time"},
	Level:        []string{"level", "severity", "log.level", "levelname"},
	Logger:       []string{"logger_name", "logger", "log.logger"},
	Thread:       []string{"thread_name", "thread", "process.thread.name"},
	Message:      []string{"message", "msg"},
	Stack:        []string{"stack_trace", "error.stack_trace", "exception"},
	TraceID:      []string{"traceId", "trace_id", "trace.id"},
	App:          []string{"app", "service.name"},
	PID:          []string{"pid", "process.pid"},
	LevelAliases: map[string]domain.Level{"50": domain.LevelError},
	Hidden:       []string{"kubernetes.*", "host*", "@version", "level_value"},
}

var src = time.Date(2026, 9, 26, 19, 0, 0, 0, time.UTC)

func raw(text string) domain.RawLine {
	return domain.RawLine{Time: src, Pod: "p-1", Container: "api", Text: text}
}

func TestLogstashLine(t *testing.T) {
	line := `{"@timestamp":"2026-09-26T18:53:10.729Z","@version":"1","message":"Request processing failed","logger_name":"io.gimle.payment.service.PaymentService","thread_name":"http-nio-8080-exec-1","level":"ERROR","level_value":40000,"stack_trace":"java.lang.IllegalStateException: boom\n\tat x.Y.z(Y.java:1)","traceId":"bc9632dd","spanId":"0011","app":"payment-service","pid":"1","kubernetes":{"namespace_name":"app-rec","pod_name":"p-1","labels":{"app":"payment"}},"extra":{"orderId":"ord_1"}}`
	e := NewJSON(logstash).Decode(raw(line))
	want := domain.LogEntry{
		Time: time.Date(2026, 9, 26, 18, 53, 10, 729e6, time.UTC), Level: domain.LevelError,
		Logger: "io.gimle.payment.service.PaymentService", Thread: "http-nio-8080-exec-1", Message: "Request processing failed",
		Stack: "java.lang.IllegalStateException: boom\n\tat x.Y.z(Y.java:1)", TraceID: "bc9632dd", App: "payment-service", PID: "1",
	}
	if !e.Time.Equal(want.Time) || e.Level != want.Level || e.Logger != want.Logger || e.Thread != want.Thread ||
		e.Message != want.Message || e.Stack != want.Stack || e.TraceID != want.TraceID || e.App != want.App || e.PID != want.PID {
		t.Fatalf("got %+v", e)
	}
	if !e.Structured || e.Pod != "p-1" || e.Raw != line {
		t.Fatal("attribution lost")
	}
	if e.Fields["spanId"] != "0011" || e.Fields["extra.orderId"] != "ord_1" || len(e.Fields) != 2 {
		t.Errorf("fields %v", e.Fields)
	}
	if e.Hidden["kubernetes.namespace_name"] != "app-rec" || e.Hidden["kubernetes.labels.app"] != "payment" || e.Hidden["@version"] != "1" || e.Hidden["level_value"] != "40000" {
		t.Errorf("hidden %v", e.Hidden)
	}
}

func TestECSNestedAndDottedKeys(t *testing.T) {
	nested := `{"@timestamp":"2026-09-26T18:53:10Z","log":{"level":"warn","logger":"a.B"},"message":"m","error":{"stack_trace":"s"},"trace":{"id":"t1"}}`
	dotted := `{"@timestamp":"2026-09-26T18:53:10Z","log.level":"warn","log.logger":"a.B","message":"m","error.stack_trace":"s","trace.id":"t1"}`
	for _, line := range []string{nested, dotted} {
		e := NewJSON(logstash).Decode(raw(line))
		if e.Level != domain.LevelWarn || e.Logger != "a.B" || e.Stack != "s" || e.TraceID != "t1" || len(e.Fields) != 0 {
			t.Errorf("%s: %+v fields %v", line, e, e.Fields)
		}
	}
}

func TestFirstCandidateWinsAndAliases(t *testing.T) {
	e := NewJSON(logstash).Decode(raw(`{"msg":"second","message":"first","severity":"50","time":1790431703123}`))
	if e.Message != "first" || e.Level != domain.LevelError {
		t.Fatalf("got %+v", e)
	}
	if e.Time.UnixMilli() != 1790431703123 {
		t.Errorf("epoch millis: %v", e.Time)
	}
	if e.Fields["msg"] != "second" {
		t.Errorf("unused candidate must stay visible: %v", e.Fields)
	}
}

func TestNonJSONFallsBackToPlain(t *testing.T) {
	d := NewJSON(logstash)
	for _, text := range []string{" :: Spring Boot ::                (v3.4.1)", `{"broken": `, `["array"]`} {
		e := d.Decode(raw(text))
		if e.Structured || e.Message != text || e.Level != domain.LevelUnknown || !e.Time.Equal(src) {
			t.Errorf("%q: %+v", text, e)
		}
	}
}

func TestPlainSpringConsoleLine(t *testing.T) {
	d := NewPlain()
	e := d.Decode(raw("2026-09-26T19:12:40.104+02:00  WARN 18472 --- [payment-service] [nio-8080-exec-7] i.g.p.gateway.GatewayClient   : latency high p95=842ms"))
	if e.Level != domain.LevelWarn || e.PID != "18472" || e.App != "payment-service" || e.Thread != "nio-8080-exec-7" ||
		e.Logger != "i.g.p.gateway.GatewayClient" || e.Message != "latency high p95=842ms" || e.Time.Hour() != 19 {
		t.Fatalf("got %+v", e)
	}
	old := d.Decode(raw("2026-09-26 19:12:40.104  INFO 1 --- [           main] o.s.b.SpringApplication : Started"))
	if old.Level != domain.LevelInfo || old.App != "" || old.Thread != "main" || old.Message != "Started" {
		t.Fatalf("pre-3.2 layout: %+v", old)
	}
}

func TestPlainLevelDetection(t *testing.T) {
	tests := map[string]domain.Level{
		"E0926 19:12:40.104123 1 reflector.go:1] failed":  domain.LevelError,
		"[WARNING] disk almost full":                      domain.LevelWarn,
		"12:00:01 ERROR something broke":                  domain.LevelError,
		"just a sentence mentioning an error much later…": domain.LevelUnknown,
	}
	for text, want := range tests {
		if got := NewPlain().Decode(raw(text)).Level; got != want {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
}

func BenchmarkJSONDecode(b *testing.B) {
	d := NewJSON(logstash)
	line := raw(`{"@timestamp":"2026-09-26T18:53:10.729Z","@version":"1","message":"request completed POST /v1/payments status=201 duration=96ms","logger_name":"io.gimle.payment.web.PaymentController","thread_name":"http-nio-8080-exec-1","level":"INFO","level_value":20000,"traceId":"bc9632ddbc9632ddbc9632ddbc9632dd","spanId":"bc9632ddbc9632dd","app":"payment-service","pid":"1","kubernetes":{"namespace_name":"app-rec","pod_name":"payment-service-7b9c8d4f6c-m8q7v","container_name":"payment-service","container_image":"eu.gcr.io/acme/payment-service:v2.14.3","host":"gke-main-pool-2-9c1f","labels":{"app.kubernetes.io/name":"payment-service","app.kubernetes.io/part-of":"payment-service"}}}`)
	b.ReportAllocs()
	for b.Loop() {
		d.Decode(line)
	}
}
