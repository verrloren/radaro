// Package netproxy applies one optional proxy to Radaro's platform and scan
// requests. An unset proxy uses Go's standard HTTP_PROXY/HTTPS_PROXY/NO_PROXY
// environment behavior.
package netproxy

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
)

var saved atomic.Pointer[url.URL]

var transport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = func(r *http.Request) (*url.URL, error) {
		if u := saved.Load(); u != nil {
			return u, nil
		}
		return http.ProxyFromEnvironment(r)
	}
	return t
}()

// Transport is shared by scan and publish clients. Proxy changes apply to new
// requests without recreating those clients.
func Transport() http.RoundTripper { return transport }

// Validate accepts a single HTTP, HTTPS or SOCKS5 proxy URL. Its error never
// includes the URL, which may contain a password.
func Validate(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Hostname() == "" || u.Port() == "" ||
		(u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("proxy must be an http, https or socks5 URL with a host and port")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return errors.New("proxy port must be between 1 and 65535")
	}
	if strings.ContainsAny(u.Hostname(), " \t\r\n") {
		return errors.New("proxy host is invalid")
	}
	return nil
}

// Set changes the proxy for future requests. Empty restores the environment.
func Set(raw string) error {
	if err := Validate(raw); err != nil {
		return err
	}
	if raw == "" {
		saved.Store(nil)
	} else {
		u, _ := url.Parse(raw)
		saved.Store(u)
	}
	transport.CloseIdleConnections()
	return nil
}
