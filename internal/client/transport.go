package client

import (
	"context"
	"net"
	"net/http"
	"net/url"

	"github.com/krwu/compose-deploy-agent/internal/credential"
)

// localEndpoint is a placeholder base URL used by RunLocal: the unix
// socket transport ignores the host, so this only needs to be a syntactically
// valid URL for http.NewRequest to build request paths against.
const localEndpoint = "http://deploy-agent"

// newUnixHTTPClient builds an http.Client that dials socketPath instead of
// resolving the request URL's host, for `deploy-agent update`. No TLS
// and no client certificate are involved: the trust root is whoever can
// reach the socket file.
func newUnixHTTPClient(socketPath string) *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &http.Client{Transport: transport}
}

// newHTTPClient builds an http.Client whose TLS config presents the parsed
// client certificate and trusts only the bundled server CA. serverName is
// derived from endpoint's host (typically an IP, matched against the server
// certificate's IPAddresses SAN).
func newHTTPClient(bundle credential.Bundle, endpoint string) (*http.Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	serverName := u.Hostname()

	transport := &http.Transport{
		TLSClientConfig: bundle.TLSConfig(serverName),
	}
	return &http.Client{Transport: transport}, nil
}
