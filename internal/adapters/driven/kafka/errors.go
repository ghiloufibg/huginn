package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/twmb/franz-go/pkg/kerr"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// classify turns a franz-go, network or TLS error into a domain error
// whose message says what to check. Credentials never appear in it.
func (s *source) classify(err error) error {
	if v := s.violation.Err(); v != nil {
		return v
	}
	brokers := strings.Join(s.conn.Bootstrap, ",")
	var (
		unknownCA x509.UnknownAuthorityError
		hostname  x509.HostnameError
		verify    *tls.CertificateVerificationError
		ke        *kerr.Error
		netErr    net.Error
		opErr     *net.OpError
		dnsErr    *net.DNSError
	)
	switch {
	case errors.Is(err, context.Canceled):
		return err
	case errors.As(err, &unknownCA):
		return fmt.Errorf("TLS: the brokers' certificate is not trusted by connection.tls.ca (%s): %w", brokers, domain.ErrUnreachable)
	case errors.As(err, &hostname):
		return fmt.Errorf("TLS: the brokers' certificate is issued for another name (%v): %w", hostname.Host, domain.ErrUnreachable)
	case errors.As(err, &verify):
		return fmt.Errorf("TLS: %v (%s): %w", verify.Err, brokers, domain.ErrUnreachable)
	case errors.As(err, &ke):
		switch ke {
		case kerr.SaslAuthenticationFailed, kerr.IllegalSaslState, kerr.UnsupportedSaslMechanism:
			return fmt.Errorf("credentials rejected by the brokers (SASL %s, user from connection.sasl.username): %w", s.conn.Mechanism, domain.ErrUnauthorized)
		case kerr.TopicAuthorizationFailed, kerr.ClusterAuthorizationFailed:
			return fmt.Errorf("not authorized: %s: %w", ke.Message, domain.ErrForbidden)
		case kerr.UnknownTopicOrPartition:
			return fmt.Errorf("unknown topic: %w", domain.ErrNotFound)
		}
		return fmt.Errorf("kafka: %s: %w", ke.Message, domain.ErrUnreachable)
	case errors.Is(err, io.EOF) && s.conn.SASL():
		// Brokers commonly just close the connection on failed
		// authentication, or when the protocol does not match.
		return fmt.Errorf("the brokers closed the connection during authentication: check connection.sasl (credentials rejected?) and connection.security (TLS required?): %w", domain.ErrUnauthorized)
	case errors.As(err, &dnsErr):
		return fmt.Errorf("brokers unreachable: cannot resolve %s: check the network or VPN: %w", dnsErr.Name, domain.ErrUnreachable)
	case errors.As(err, &opErr), errors.As(err, &netErr), errors.Is(err, context.DeadlineExceeded), errors.Is(err, net.ErrClosed):
		return fmt.Errorf("brokers unreachable (%s): check the network or VPN: %v: %w", brokers, err, domain.ErrUnreachable)
	}
	return fmt.Errorf("kafka (%s): %v: %w", brokers, err, domain.ErrUnreachable)
}

// topicError turns the error code of a topic or partition into a domain
// error.
func topicError(topic string, code int16) error {
	switch err := kerr.ErrorForCode(code); err {
	case kerr.UnknownTopicOrPartition:
		return fmt.Errorf("topic %s: %w", topic, domain.ErrNotFound)
	case kerr.TopicAuthorizationFailed:
		return fmt.Errorf("topic %s: not authorized: %w", topic, domain.ErrForbidden)
	default:
		return fmt.Errorf("topic %s: %v: %w", topic, err, domain.ErrUnreachable)
	}
}
