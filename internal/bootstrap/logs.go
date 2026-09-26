package bootstrap

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/ghiloufibg/huginn/internal/adapters/driven/logformat"
	"github.com/ghiloufibg/huginn/internal/adapters/driven/springlayout"
	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/app"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// decoderRegistry lists the decoders selectable with log_formats.*.decoder.
func decoderRegistry() *ports.Registry[func(config.LogFormat) (ports.LogDecoder, error)] {
	r := ports.NewRegistry[func(config.LogFormat) (ports.LogDecoder, error)]("log decoder")
	r.Register("json-fields", func(f config.LogFormat) (ports.LogDecoder, error) {
		p, err := profile(f)
		if err != nil {
			return nil, err
		}
		return logformat.NewJSON(p), nil
	})
	r.Register("plain", func(config.LogFormat) (ports.LogDecoder, error) { return logformat.NewPlain(), nil })
	return r
}

// rendererRegistry lists the layouts selectable with logs.renderer.
func rendererRegistry() *ports.Registry[func() ports.LogRenderer] {
	r := ports.NewRegistry[func() ports.LogRenderer]("log renderer")
	r.Register("spring-compact", func() ports.LogRenderer { return springlayout.NewCompact() })
	r.Register("spring-full", func() ports.LogRenderer { return springlayout.NewFull() })
	return r
}

// profile converts a configured log format into a decoder profile.
func profile(f config.LogFormat) (logformat.Profile, error) {
	aliases := map[string]domain.Level{}
	for k, v := range f.LevelAliases {
		l, ok := domain.ParseLevel(v)
		if !ok {
			return logformat.Profile{}, fmt.Errorf("level alias %q: unknown level %q", k, v)
		}
		aliases[strings.ToLower(k)] = l
	}
	fm := f.Fields
	return logformat.Profile{
		Timestamp: fm.Timestamp, Level: fm.Level, Logger: fm.Logger, Thread: fm.Thread, Message: fm.Message,
		Stack: fm.Stack, TraceID: fm.TraceID, App: fm.App, PID: fm.PID,
		LevelAliases: aliases, Hidden: f.Hidden,
	}, nil
}

// logParts builds the decoder and the stream renderer from configuration.
func logParts(c *config.Config) (ports.LogDecoder, ports.LogRenderer, error) {
	f := c.LogFormats[c.Logs.Format]
	newDecoder, err := decoderRegistry().Lookup(f.Decoder)
	if err != nil {
		return nil, nil, err
	}
	dec, err := newDecoder(f)
	if err != nil {
		return nil, nil, fmt.Errorf("log format %q: %w", c.Logs.Format, err)
	}
	newRenderer, err := rendererRegistry().Lookup(c.Logs.Renderer)
	if err != nil {
		return nil, nil, err
	}
	return dec, newRenderer(), nil
}

func newLogSessions(c *config.Config, cluster Cluster, clock ports.Clock, filter domain.ContainerFilter, dec ports.LogDecoder, log *slog.Logger) *app.LogSessions {
	return &app.LogSessions{
		Cluster: cluster, Logs: cluster, Resolver: resolverChain(c, log), Scopes: scopes(c),
		Filter: filter, Decoder: dec, Clock: clock, Log: log, MaxHistory: c.Logs.BufferLines,
	}
}
