package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// kafkaZoomScreen shows one record in full: where it is, its key, its
// headers and its value laid out for reading.
type kafkaZoomScreen struct {
	records *kafkaRecordsScreen
	seq     uint64
	offset  int
	height  int
	cached  uint64   // seq of the record laid out in lines
	cache   []string // its lines: the value is laid out once, not per frame
}

func newKafkaZoomScreen(r *kafkaRecordsScreen, seq uint64) *kafkaZoomScreen {
	return &kafkaZoomScreen{records: r, seq: seq}
}

func (z *kafkaZoomScreen) crumbs() []string {
	c := z.records.crumbs()
	if rec, ok := z.record(); ok {
		return append(c, fmt.Sprintf("p%d #%d", rec.Partition, rec.Offset))
	}
	return append(c, "record")
}

func (z *kafkaZoomScreen) record() (*domain.KafkaRecord, bool) {
	i, ok := z.records.buf.Index(z.seq)
	if !ok {
		return nil, false
	}
	return z.records.buf.At(i), true
}

func (z *kafkaZoomScreen) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	keys, key := m.opts.Keys, k.String()
	page := max(z.height-1, 1)
	switch {
	case keys.Is(key, ActDown):
		z.offset++
	case keys.Is(key, ActUp):
		z.offset--
	case keys.Is(key, ActPageDown):
		z.offset += page
	case keys.Is(key, ActPageUp):
		z.offset -= page
	case keys.Is(key, ActTop):
		z.offset = 0
	case keys.Is(key, ActNextEntry):
		z.step(1)
	case keys.Is(key, ActPrevEntry):
		z.step(-1)
	case keys.Is(key, ActCopy):
		if rec, ok := z.record(); ok {
			return true, copyRecord(m, rec)
		}
	default:
		return false, nil
	}
	z.offset = max(z.offset, 0)
	return true, nil
}

// step moves to the next or previous record shown on the records screen.
func (z *kafkaZoomScreen) step(dir int) {
	r := z.records
	for i, seq := range r.rows {
		if seq == z.seq && i+dir >= 0 && i+dir < len(r.rows) {
			z.seq, z.offset = r.rows[i+dir], 0
			r.cursor, r.tail = i+dir, false
			return
		}
	}
}

func (z *kafkaZoomScreen) lines(t Theme) []string {
	rec, ok := z.record()
	if !ok {
		return []string{t.Dim.Render("this record left the buffer (older records are dropped first)")}
	}
	field := func(name, value string) string { return t.Dim.Render(fmt.Sprintf("%-11s", name)) + value }
	ts := "none"
	if !rec.Time.IsZero() {
		kind := "producer time"
		if rec.LogAppendTime {
			kind = "broker time"
		}
		ts = rec.Time.In(time.Local).Format("2006-01-02 15:04:05.000 MST") + t.Dim.Render("  ("+kind+")")
	}
	size := domain.ByteSize(rec.ValueSize)
	if rec.Truncated() {
		size += t.Warn.Render(fmt.Sprintf("  (truncated: %s kept)", domain.ByteSize(len(rec.Value))))
	}
	out := []string{
		field("topic", rec.Topic),
		field("partition", fmt.Sprint(rec.Partition)) + t.Dim.Render("   offset ") + fmt.Sprint(rec.Offset),
		field("time", ts),
		field("key", domain.PayloadPreview(rec.Key, rec.KeySize, 200)),
		field("value", size),
	}
	if len(rec.Headers) > 0 {
		out = append(out, "", t.TableHeader.Render("HEADERS"))
		for _, h := range rec.Headers {
			out = append(out, "  "+t.Dim.Render(domain.PayloadPreview([]byte(h.Key), len(h.Key), 40)+": ")+domain.PayloadPreview(h.Value, len(h.Value), 200))
		}
	}
	out = append(out, "", t.TableHeader.Render("VALUE"))
	if rec.Value == nil {
		return append(out, "  tombstone (null value: the key was deleted)")
	}
	if k, id := domain.ClassifyPayload(rec.Value); k == domain.PayloadFramed {
		out = append(out, t.Dim.Render(fmt.Sprintf("  schema registry framing, schema id %d: not decoded", id)))
	}
	for _, l := range domain.PayloadLines(rec.Value) {
		out = append(out, "  "+l)
	}
	return out
}

func (z *kafkaZoomScreen) view(m *Model, w, h int) string {
	z.height = h
	if z.cache == nil || z.cached != z.seq {
		z.cache, z.cached = z.lines(m.opts.Theme), z.seq
	}
	lines := slices.Clone(z.cache)
	z.offset = min(z.offset, max(len(lines)-h, 0))
	end := min(z.offset+h, len(lines))
	shown := lines[z.offset:end]
	for i, l := range shown {
		shown[i] = " " + ansi.Truncate(l, w-1, "…")
	}
	return strings.Join(shown, "\n")
}

func (z *kafkaZoomScreen) statusLeft(m *Model) string {
	t := m.opts.Theme
	bar := t.Status
	if m.env.Production {
		bar = t.StatusProd
	}
	return t.Chip.Render("RECORD") + bar.Render("  read only")
}

func (z *kafkaZoomScreen) hints(m *Model) []hint {
	return []hint{m.pair(ActNextEntry, ActPrevEntry, "next/previous record"), m.pair(ActDown, ActUp, "scroll"), m.h(ActCopy, "copy value"), m.h(ActBack, "back")}
}

func (z *kafkaZoomScreen) prompt(*Model) string { return "" }
