package smh

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"syscall"
)

// classifyTransportError retains only a fixed diagnostic category. Original
// errors can carry signed URLs, credentials, DNS names or certificate details.
// Categories do not establish whether a mutation reached the remote server.
func classifyTransportError(err error) error {
	// url.Error.Timeout only examines its immediate cause. Remove URL wrappers
	// before looking for a nested network error behind other contextual wrapping.
	var request *url.Error
	for errors.As(err, &request) {
		err = request.Err
	}
	var network net.Error
	var dns *net.DNSError
	var certificate *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	category := ""
	switch {
	case errors.As(err, &network) && network.Timeout():
		category = "timeout"
	case errors.As(err, &dns):
		category = "dns"
	case errors.As(err, &certificate), errors.As(err, &record), errors.As(err, &authority), errors.As(err, &hostname):
		category = "tls"
	case errors.Is(err, syscall.ECONNREFUSED):
		category = "connection refused"
	case errors.Is(err, syscall.ECONNRESET):
		category = "connection reset"
	case errors.Is(err, syscall.ENETUNREACH):
		category = "network unreachable"
	case errors.Is(err, syscall.EHOSTUNREACH):
		category = "host unreachable"
	case errors.Is(err, io.ErrUnexpectedEOF):
		category = "unexpected EOF"
	case errors.Is(err, io.EOF):
		category = "EOF"
	default:
		return ErrTransport
	}
	return fmt.Errorf("SMH transport %s: %w", category, ErrTransport)
}
