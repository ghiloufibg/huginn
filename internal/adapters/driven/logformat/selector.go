package logformat

import (
	"path"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Rule gives the decoder, and the muted loggers, of the containers it
// matches. Empty glob lists match anything.
type Rule struct {
	Repos, Containers []string
	Decoder           ports.LogDecoder
	Mute              *domain.LoggerMute
}

func (r Rule) matches(repo, container string) bool {
	return anyGlob(r.Repos, repo) && anyGlob(r.Containers, container)
}

func anyGlob(globs []string, s string) bool {
	if len(globs) == 0 {
		return true
	}
	for _, g := range globs {
		if ok, _ := path.Match(g, s); ok {
			return true
		}
	}
	return false
}

// Selector implements ports.LogDecoders: the first rule matching the
// repository and container wins, else Fallback.
type Selector struct {
	Rules    []Rule
	Fallback ports.LogDecoder
}

// For implements ports.LogDecoders.
func (s Selector) For(repo, container string) ports.LogFormat {
	for _, r := range s.Rules {
		if r.matches(repo, container) {
			return ports.LogFormat{Decoder: r.Decoder, Mute: r.Mute}
		}
	}
	return ports.LogFormat{Decoder: s.Fallback}
}
