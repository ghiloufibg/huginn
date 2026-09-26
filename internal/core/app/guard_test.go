package app

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

type panicky struct{}

func (panicky) Decode(domain.RawLine) domain.LogEntry { panic("unexpected line") }

func TestSafeDecodeKeepsTheLine(t *testing.T) {
	e := safeDecode(panicky{}, domain.RawLine{Pod: "p", Text: "weird"})
	if e.Message != "weird" || e.Structured || e.Pod != "p" {
		t.Fatalf("got %+v", e)
	}
}

func TestRecoveredReportsPanics(t *testing.T) {
	var got error
	func() {
		defer recovered(slog.New(slog.DiscardHandler), "unit", func(err error) { got = err })
		panic(errors.New("boom"))
	}()
	if got == nil || !strings.Contains(got.Error(), "unit: internal error") || !strings.Contains(got.Error(), "boom") {
		t.Fatalf("got %v", got)
	}
}
