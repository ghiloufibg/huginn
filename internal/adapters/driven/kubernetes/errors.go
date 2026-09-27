package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	return fmt.Errorf("%v: %w", err, kind)
}

func kindOf(err error) error {
	var netErr net.Error
	msg := err.Error()
	switch {
	case apierrors.IsUnauthorized(err):
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
		apierrors.IsTooManyRequests(err), apierrors.IsInternalError(err), errors.As(err, &netErr),
		strings.Contains(msg, "connection refused"), strings.Contains(msg, "no such host"):
		return domain.ErrUnreachable
	}
	return nil
}
