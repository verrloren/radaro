package redditbrowser

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/verrloren/radaro/internal/netproxy"
	"golang.org/x/net/proxy"
)

// Chromium does not support authenticated SOCKS proxies. A private loopback
// bridge also keeps proxy passwords out of Chromium's command line.
func proxyBridge(raw string) (string, func(), error) {
	if err := netproxy.Validate(raw); err != nil {
		return "", nil, err
	}
	if raw == "" {
		return "", func() {}, nil
	}
	u, _ := url.Parse(raw)
	dial := func(ctx context.Context, target string) (net.Conn, error) {
		d := &net.Dialer{Timeout: 15 * time.Second}
		if u.Scheme == "socks5" {
			var auth *proxy.Auth
			if u.User != nil {
				p, _ := u.User.Password()
				auth = &proxy.Auth{User: u.User.Username(), Password: p}
			}
			p, err := proxy.SOCKS5("tcp", u.Host, auth, d)
			if err != nil {
				return nil, errors.New("proxy connection failed")
			}
			return p.(proxy.ContextDialer).DialContext(ctx, "tcp", target)
		}
		var c net.Conn
		var err error
		if u.Scheme == "https" {
			c, err = (&tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", u.Host)
		} else {
			c, err = d.DialContext(ctx, "tcp", u.Host)
		}
		if err != nil {
			return nil, errors.New("proxy connection failed")
		}
		_ = c.SetDeadline(time.Now().Add(15 * time.Second))
		req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
		if u.User != nil {
			p, _ := u.User.Password()
			req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(u.User.Username()+":"+p)))
		}
		if err = req.Write(c); err != nil {
			c.Close()
			return nil, errors.New("proxy connection failed")
		}
		br := bufio.NewReader(c)
		resp, err := http.ReadResponse(br, req)
		if err != nil || resp.StatusCode != http.StatusOK {
			c.Close()
			return nil, errors.New("proxy refused the connection")
		}
		_ = c.SetDeadline(time.Time{})
		return &bufferedConn{Conn: c, Reader: br}, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, errors.New("could not start the account proxy")
	}
	ctx, cancel := context.WithCancel(context.Background())
	transport := &http.Transport{DialContext: func(c context.Context, _, addr string) (net.Conn, error) { return dial(c, addr) }}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			req := r.Clone(ctx)
			req.RequestURI = ""
			req.Header.Del("Proxy-Authorization")
			resp, err := transport.RoundTrip(req)
			if err != nil {
				http.Error(w, "account proxy failed", http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			for k, vs := range resp.Header {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, resp.Body)
			return
		}
		up, err := dial(ctx, r.Host)
		if err != nil {
			http.Error(w, "account proxy failed", http.StatusBadGateway)
			return
		}
		down, br, err := w.(http.Hijacker).Hijack()
		if err != nil {
			up.Close()
			return
		}
		_, _ = br.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = br.Flush()
		stop := context.AfterFunc(ctx, func() { down.Close(); up.Close() })
		defer stop()
		defer down.Close()
		defer up.Close()
		go func() { _, _ = io.Copy(up, br); up.Close() }()
		_, _ = io.Copy(down, up)
	})}
	go func() { _ = srv.Serve(ln) }()
	return "http://" + ln.Addr().String(), func() { cancel(); _ = srv.Close(); transport.CloseIdleConnections() }, nil
}

type bufferedConn struct {
	net.Conn
	*bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.Reader.Read(p) }
