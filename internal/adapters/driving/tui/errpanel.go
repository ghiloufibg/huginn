package tui

import (
	"errors"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// maxPanelWidth keeps an error panel readable on wide terminals.
const maxPanelWidth = 76

// errorPanel lays out the screen of an error that leaves nothing else to
// show: a headline, the error's own message, what to do, then the keys.
// Every part is wrapped to the width; when the height is short, the
// message is cut first, so the advice and the keys stay visible.
func errorPanel(t *Theme, title string, err error, fix, keys string, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	tw := min(maxPanelWidth, max(w-4, min(w, 20)))
	wrap := func(s string, style lipgloss.Style) []string {
		lines := strings.Split(ansi.Wrap(s, tw, "/"), "\n")
		for i, l := range lines {
			lines[i] = style.Render(l)
		}
		return lines
	}
	head := wrap(title, t.Bad.Bold(true))
	var detail, tail []string
	if !bareKind(err) {
		detail = wrap(err.Error(), t.Dim)
	}
	for _, s := range []string{fix, keys} {
		if s != "" {
			tail = append(tail, "")
			tail = append(tail, wrap(s, lipgloss.NewStyle())...)
		}
	}
	if room := h - len(head) - len(tail) - 1; len(detail) > 0 && len(detail) > room {
		if room < 1 {
			detail = nil
		} else {
			detail = detail[:room]
			detail[room-1] = t.Dim.Render(ansi.Truncate(ansi.Strip(detail[room-1]), tw-1, "") + "…")
		}
	}
	lines := head
	if len(detail) > 0 {
		lines = append(append(lines, ""), detail...)
	}
	lines = append(lines, tail...)
	if len(lines) > h {
		lines = lines[:max(h, 0)]
	}
	// One block: the headline centered over the left-aligned text.
	bw := 0
	for _, l := range lines {
		bw = max(bw, ansi.StringWidth(l))
	}
	for i, l := range lines {
		pos := lipgloss.Left
		if i < len(head) {
			pos = lipgloss.Center
		}
		lines[i] = lipgloss.PlaceHorizontal(bw, pos, l)
	}
	return centered(strings.Join(lines, "\n"), w, h)
}

// bareKind tells an error that is only its kind (domain.ErrUnauthorized
// itself): its message would repeat the headline.
func bareKind(err error) bool {
	return errors.Unwrap(err) == nil && errKind(err) != "error"
}

// errTitle heads the error screen of an environment: what failed, in the
// words of the error kind.
func errTitle(failed, env string, err error) string {
	if errors.Is(err, domain.ErrUnauthorized) {
		return "Not logged in to " + env
	}
	return failed + " " + env + ": " + errKind(err)
}

// errFix tells what the user can do about a cluster error, or "" when
// waiting is all there is to it.
func errFix(t *Theme, err error) string {
	switch {
	case domain.Permanent(err):
		return "Fix the setup: environments.yaml, the kubeconfig or its credential plugin."
	case errors.Is(err, domain.ErrUnauthorized):
		return "Log in again: on GKE, run " + t.Key.Render("gcloud auth login") + "."
	case errors.Is(err, domain.ErrForbidden):
		return "Ask for read access to this namespace."
	case errors.Is(err, domain.ErrUnreachable):
		return "Check your network or VPN access to the cluster."
	}
	return ""
}

// retryKeys is the key line of an environment error screen: permanent
// errors are not retried, the others are, until the user acts.
func (m *Model) retryKeys(err error) string {
	t := &m.opts.Theme
	if domain.Permanent(err) {
		return t.Dim.Render("press ") + t.Key.Render(m.label(ActRefresh)) + t.Dim.Render(" to retry")
	}
	return t.Dim.Render("retrying automatically · press ") + t.Key.Render(m.label(ActRefresh)) + t.Dim.Render(" to retry now")
}
