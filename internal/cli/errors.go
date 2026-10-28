package cli

import (
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/config"
)

// friendlyErr translates API errors into actionable messages.
func friendlyErr(err error) string {
	var notConfigured *config.ErrNotConfigured
	if errors.As(err, &notConfigured) {
		return err.Error()
	}
	switch {
	case errors.Is(err, api.ErrTruncatedStream):
		return "the connection ended before the answer was complete — send the prompt again (the agent's server-side memory is intact)"
	case errors.Is(err, api.ErrStreamStalled):
		return "the server stopped sending data mid-answer — check that it is healthy, or raise " + api.EnvStreamIdleTimeout + " for very slow models"
	}
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case 429:
			return "the server is rate limiting requests (HTTP 429): " + apiErr.Message + " — wait a moment and try again"
		case 500, 502, 503, 504:
			return fmt.Sprintf("the turnlith server had a problem (HTTP %d): %s", apiErr.Status, apiErr.Message)
		case 401:
			return "authentication failed — the api key was rejected (run `tlc configure`)"
		case 402:
			return "insufficient credits on the turnlith account: " + apiErr.Message
		case 403:
			return "this api key cannot manage agents — creating agents, switching models and setting effort need an admin-role key (mint one in the console under API keys, or edit the agent there)"
		case 404:
			if apiErr.Code == "model_not_found" {
				return apiErr.Message + " — run `tlc agents` to list the workspace's agents"
			}
		}
		return err.Error()
	}
	if errors.Is(err, io.EOF) {
		return "the server closed the connection unexpectedly"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		var unknownAuthority x509.UnknownAuthorityError
		var certInvalid x509.CertificateInvalidError
		var hostErr x509.HostnameError
		switch {
		case errors.As(err, &unknownAuthority), errors.As(err, &certInvalid), errors.As(err, &hostErr):
			return fmt.Sprintf("TLS certificate problem talking to %s: %v", urlHost(urlErr.URL), urlErr.Err)
		case isTimeout(err):
			return fmt.Sprintf("the request to %s timed out — is the server reachable?", urlHost(urlErr.URL))
		default:
			return fmt.Sprintf("cannot reach %s: %v — check the URL (`tlc configure`) and that the server is running", urlHost(urlErr.URL), urlErr.Err)
		}
	}
	return err.Error()
}

// retryReason names, in a few words, why the client is about to retry.
func retryReason(err error) string {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case 429:
			return "rate limited by the server"
		case 502, 503:
			return "server unavailable"
		}
	}
	return "cannot connect to the server"
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// urlHost shows just scheme://host of a request URL, never a path or query.
func urlHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Scheme + "://" + u.Host
}
