package domain

import "testing"

func TestLoggerMute(t *testing.T) {
	m, err := NewLoggerMute([]string{"com.example.pool.Pool", "com.example.metrics.*", "Snap*"}, []Level{LevelError})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		logger string
		level  Level
		want   bool
	}{
		{"com.example.pool.Pool", LevelInfo, true},
		{"com.example.pool.PoolBase", LevelInfo, false}, // exact name only
		{"com.example.pool", LevelInfo, false},
		{"com.example.metrics.Resources", LevelDebug, true},
		{"com.example.metrics.jvm.Gc", LevelWarn, true},
		{"com.example.metricsX", LevelInfo, false},
		{"SnapshotTask", LevelUnknown, true},
		{"snapshotTask", LevelInfo, false},           // case-sensitive
		{"com.example.pool.Pool", LevelError, false}, // kept level
		{"", LevelInfo, false},                       // no logger
		{"com.example.Service", LevelInfo, false},
	}
	for _, tt := range tests {
		e := LogEntry{Logger: tt.logger, Level: tt.level}
		if got := m.Mutes(&e); got != tt.want {
			t.Errorf("Mutes(%q, %v) = %v, want %v", tt.logger, tt.level, got, tt.want)
		}
	}
}

func TestLoggerMuteMatchNamesThePattern(t *testing.T) {
	m, err := NewLoggerMute([]string{"com.example.*", "com.example.pool.Pool"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for logger, want := range map[string]string{
		"com.example.pool.Pool": "com.example.pool.Pool", // exact wins
		"com.example.Other":     "com.example.*",
		"org.Other":             "",
	} {
		if got, _ := m.Match(&LogEntry{Logger: logger}); got != want {
			t.Errorf("Match(%q) = %q, want %q", logger, got, want)
		}
	}
}

func TestLoggerMuteNil(t *testing.T) {
	m, err := NewLoggerMute(nil, nil)
	if err != nil || m != nil {
		t.Fatalf("NewLoggerMute(nil) = %v, %v; want nil, nil", m, err)
	}
	if m.Mutes(&LogEntry{Logger: "a"}) {
		t.Error("a nil mute muted an entry")
	}
}

func TestCheckMutePattern(t *testing.T) {
	for p, ok := range map[string]bool{
		"com.example.Pool": true, "com.example.*": true, "a*": true,
		"": false, "*": false, "com.*.Pool": false, "a**": false, "com example": false,
	} {
		if err := CheckMutePattern(p); (err == nil) != ok {
			t.Errorf("CheckMutePattern(%q) = %v, want ok=%v", p, err, ok)
		}
	}
	if _, err := NewLoggerMute([]string{"ok", "*"}, nil); err == nil {
		t.Error("NewLoggerMute accepted *")
	}
}

func BenchmarkLoggerMute(b *testing.B) {
	patterns := []string{"com.example.pool.Pool", "com.example.metrics.Snapshot"}
	for i := range 18 {
		patterns = append(patterns, "org.lib"+string(rune('a'+i))+".*")
	}
	m, err := NewLoggerMute(patterns, []Level{LevelError})
	if err != nil {
		b.Fatal(err)
	}
	e := LogEntry{Logger: "com.example.orders.OrderService", Level: LevelInfo}
	muted := LogEntry{Logger: "org.libr.Pool", Level: LevelInfo}
	b.ReportAllocs()
	for b.Loop() {
		m.Mutes(&e)
		m.Match(&muted)
	}
}
