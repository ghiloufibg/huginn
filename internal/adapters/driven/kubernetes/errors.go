package kubernetes

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"regexp"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// mapErr turns a client-go error into a domain error kind, keeping the
// API's message (which names the resource and the missing permission).
func mapErr(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	kind := kindOf(err)
	if kind == nil {
		return err
	}
	msg := err.Error()
	if i := strings.Index(msg, credentialFailure); i >= 0 {
		msg = credentialMessage(msg[i:])
	}
	return domain.KindError(kind, msg)
}

// credentialMessage rewrites client-go's message about a failed credential
// plugin for the screen. The request ("Get "https://…/pods?limit=500": ")
// is dropped: the credentials failed before anything was sent. The
// plugin's name is enough, its full path only makes the message wrap. A
// missing plugin keeps the kubeconfig's install hint, not client-go's
// generic help; a failed one gets the reason it printed.
func credentialMessage(msg string) string {
	msg = pluginPath.ReplaceAllStringFunc(msg, func(s string) string {
		verb, path, _ := strings.Cut(s, " ")
		colon := strings.HasSuffix(path, ":")
		path = filepath.Base(strings.TrimSuffix(path, ":"))
		if colon {
			path += ":"
		}
		return verb + " " + path
	})
	if missingPlugin(msg) {
		paras := strings.Split(msg, "\n\n")
		first := paras[0]
		if len(paras) > 3 { // message, generic help (2 paragraphs), install hint
			first += ". " + strings.Join(strings.Fields(paras[len(paras)-1]), " ")
		}
		return first
	}
	if strings.Contains(msg, "failed with exit code") {
		if reason := pluginReason(); reason != "" {
			msg += ": " + reason
		}
	}
	return msg
}

// credentialFailure starts client-go's message when the kubeconfig's
// credential plugin fails (not logged in, expired session). The HTTP
// client wraps it in a *url.Error, which would read as a network error.
const credentialFailure = "getting credentials: "

// missingPlugin tells, from the first line of client-go's message, that the
// credential plugin is not installed: retrying cannot help. A bare name
// not in PATH reads "executable X not found"; a path, the system's error.
func missingPlugin(msg string) bool {
	first, _, _ := strings.Cut(msg, "\n")
	for _, s := range []string{" not found", "no such file or directory", "cannot find the file"} {
		if strings.Contains(first, s) {
			return true
		}
	}
	return false
}

// pluginPath is the credential plugin's command in client-go's messages.
var pluginPath = regexp.MustCompile(`(executable|fork/exec) \S+`)

func kindOf(err error) error {
	var netErr net.Error
	msg := err.Error()
	switch {
	case strings.Contains(msg, credentialFailure) && missingPlugin(msg):
		return domain.ErrConfig
	case apierrors.IsUnauthorized(err), strings.Contains(msg, credentialFailure):
		return domain.ErrUnauthorized
	case apierrors.IsForbidden(err):
		return domain.ErrForbidden
	case apierrors.IsNotFound(err):
		return domain.ErrNotFound
	case apierrors.IsBadRequest(err) && (strings.Contains(msg, "waiting to start") || strings.Contains(msg, "ContainerCreating") || strings.Contains(msg, "PodInitializing")):
		return domain.ErrNotStarted
	case apierrors.IsBadRequest(err) && strings.Contains(msg, "previous terminated container"):
		return domain.ErrNotFound // no previous instance
	case apierrors.IsTimeout(err), apierrors.IsServerTimeout(err), apierrors.IsServiceUnavailable(err),
		apierrors.IsTooManyRequests(err), apierrors.IsInternalError(err), errors.As(err, &netErr):
		return domain.ErrUnreachable
	}
	for _, s := range networkFailures {
		if strings.Contains(msg, s) {
			return domain.ErrUnreachable
		}
	}
	return nil
}

// networkFailures are messages of connections that broke (the HTTP/2
// transport and the watch decoder wrap them in plain errors).
var networkFailures = []string{
	"connection refused", "no such host", "connection reset", "broken pipe", "i/o timeout",
	"http2: client connection lost", "unable to decode an event from the watch stream",
	"TLS handshake timeout", "use of closed network connection", "unexpected EOF",
}
