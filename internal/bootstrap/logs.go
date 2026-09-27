package bootstrap

import (
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/ghiloufibg/huginn/internal/adapters/driven/layout"
	"github.com/ghiloufibg/huginn/internal/adapters/driven/logformat"
	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/app"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// decoderRegistry lists the decoders selectable with formats/*.yaml
// decoder. Adding one is an adapter plus a line here.
func decoderRegistry() *ports.Registry[func(config.Format) ports.LogDecoder] {
	r := ports.NewRegistry[func(config.Format) ports.LogDecoder]("log decoder")
	r.Register("json", func(f config.Format) ports.LogDecoder {
		fm := f.Fields
		return logformat.NewJSON(logformat.Profile{
			Name: f.Name, Timestamp: fm.Time, Level: fm.Level, Logger: fm.Logger, Thread: fm.Thread, Message: fm.Message,
			Stack: fm.Stack, TraceID: fm.TraceID, App: fm.App, PID: fm.PID, LevelAliases: levelAliases(f), Hidden: f.Hidden,
		})
	})
	r.Register("regex", func(f config.Format) ports.LogDecoder {
		p := logformat.RegexProfile{
			Name: f.Name, Pattern: regexp.MustCompile(f.Pattern), TimeFormat: f.TimeFormat,
			LevelAliases: levelAliases(f), LevelField: f.LevelFrom.Field,
		}
		for glob, lvl := range f.LevelFrom.Map {
			l, _ := domain.ParseLevel(lvl)
			p.LevelRules = append(p.LevelRules, logformat.LevelRule{Glob: glob, Level: l})
		}
		slices.SortFunc(p.LevelRules, func(a, b logformat.LevelRule) int { return strings.Compare(a.Glob, b.Glob) })
		return logformat.NewRegex(p)
	})
	r.Register("plain", func(f config.Format) ports.LogDecoder { return logformat.NewPlain(f.Name) })
	return r
}

// levelAliases turns the levels section (level → spellings) into the
// decoders' spelling → level map. The folder is validated, so levels parse.
func levelAliases(f config.Format) map[string]domain.Level {
	out := map[string]domain.Level{}
	for lvl, spellings := range f.Levels {
		l, _ := domain.ParseLevel(lvl)
		for _, s := range spellings {
			out[strings.ToLower(s)] = l
		}
	}
	return out
}

// roles maps the role names of layouts/*.yaml to render roles.
var roles = map[string]ports.Role{
	"time": ports.RoleTimestamp, "level": ports.RoleLevel, "thread": ports.RoleThread, "logger": ports.RoleLogger,
	"pid": ports.RolePID, "dim": ports.RoleDim, "plain": ports.RolePlain,
}

func lineSpec(l config.Line) layout.LineSpec {
	s := layout.LineSpec{TimeFormat: l.TimeFormat, Separator: l.Separator.Text, SeparatorAfter: l.Separator.After}
	for _, c := range l.Columns {
		s.Columns = append(s.Columns, layout.ColumnSpec{
			Name: c.Name, Key: c.Key, Show: c.Show, Role: roles[c.Role], HideBelow: c.HideBelow, Visible: c.Visible == nil || *c.Visible,
		})
	}
	return s
}

// logging is what the log pipeline needs from the folder.
type logging struct {
	decoders ports.LogDecoders
	layouts  map[string]ports.LogLayout // by format name
	fallback ports.LogLayout
	columns  []ports.ColumnSpec
}

// compileLayouts compiles the layouts of the folder. Template errors are
// returned as problems located in the layout file.
func compileLayouts(c *config.Config) (map[string]*layout.Layout, []config.Problem) {
	var probs []config.Problem
	compiled := map[string]*layout.Layout{}
	for _, name := range slices.Sorted(maps.Keys(c.Layouts)) {
		l := c.Layouts[name]
		lo, errs := layout.New(layout.Spec{Stream: lineSpec(l.Stream), Zoom: lineSpec(l.Zoom), FrameworkPrefixes: l.Stack.FrameworkPrefixes})
		for _, e := range errs {
			if l.ZoomIsStream && strings.HasPrefix(e.Path, "zoom.") {
				continue // the same template, already reported for stream
			}
			probs = append(probs, c.Problem(l.File, e.Path, "%s", e.Msg))
		}
		compiled[name] = lo
	}
	return compiled, probs
}

// logParts builds the decoders and layouts of a valid folder.
func logParts(c *config.Config) (logging, []config.Problem) {
	compiled, probs := compileLayouts(c)
	if len(probs) > 0 {
		return logging{}, probs
	}
	lp := logging{layouts: map[string]ports.LogLayout{}}
	var sel logformat.Selector
	seen := map[string]bool{}
	decoders := decoderRegistry()
	for _, f := range c.Formats {
		newDecoder, err := decoders.Lookup(f.Decoder)
		if err != nil {
			probs = append(probs, c.Problem(f.File, "decoder", "%v", err))
			continue
		}
		sel.Rules = append(sel.Rules, logformat.Rule{Repos: f.Match.Repos, Containers: f.Match.Containers, Decoder: newDecoder(f)})
		lo := compiled[f.Layout]
		lp.layouts[f.Name] = lo
		if lp.fallback == nil {
			lp.fallback = lo
		}
		for _, col := range lo.Columns() {
			if !seen[col.Name] {
				seen[col.Name] = true
				lp.columns = append(lp.columns, col)
			}
		}
	}
	sel.Fallback = logformat.NewPlain("")
	lp.decoders = sel
	if len(probs) > 0 {
		return logging{}, probs
	}
	return lp, nil
}

func newLogSessions(c *config.Config, sc app.ScopeFunc, cluster Cluster, clock ports.Clock, filter domain.ContainerFilter, dec ports.LogDecoders, log *slog.Logger) *app.LogSessions {
	return &app.LogSessions{
		Standalone: c.Services.ShowStandalone(),
		Cluster:    cluster, Logs: cluster, Resolver: resolverChain(c, log), Scopes: sc,
		Filter: filter, Decoders: dec, Clock: clock, Log: log, MaxHistory: c.Huginn.Logs.BufferLines,
	}
}

// newPodEvents reads pod events on demand for the services preview.
func newPodEvents(sc app.ScopeFunc, cluster ports.ClusterClient) *app.PodEvents {
	return &app.PodEvents{Cluster: cluster, Scopes: sc}
}
