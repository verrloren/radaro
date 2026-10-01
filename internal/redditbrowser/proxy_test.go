package redditbrowser

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProxyBridgeAuthenticatesAndDoesNotFallBack(t *testing.T) {
	var targetCalls, proxyCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		_, _ = io.WriteString(w, "through account proxy")
	}))
	defer target.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		if r.Method != "CONNECT" || r.Header.Get("Proxy-Authorization") != "Basic dXNlcjpwYXNz" {
			w.WriteHeader(407)
			return
		}
		up, err := net.Dial("tcp", r.Host)
		if err != nil {
			t.Error(err)
			w.WriteHeader(502)
			return
		}
		down, br, err := w.(http.Hijacker).Hijack()
		if err != nil {
			up.Close()
			return
		}
		defer down.Close()
		defer up.Close()
		_, _ = br.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = br.Flush()
		go func() { _, _ = io.Copy(up, br); up.Close() }()
		_, _ = io.Copy(down, up)
	}))
	defer upstream.Close()
	bridge, close, err := proxyBridge(strings.Replace(upstream.URL, "http://", "http://user:pass@", 1))
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	u, _ := url.Parse(bridge)
	tr := &http.Transport{Proxy: http.ProxyURL(u)}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr}).Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "through account proxy" || proxyCalls.Load() != 1 || targetCalls.Load() != 1 {
		t.Fatal("account did not use its authenticated proxy")
	}
	bad, stop, err := proxyBridge(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	u, _ = url.Parse(bad)
	tr2 := &http.Transport{Proxy: http.ProxyURL(u)}
	defer tr2.CloseIdleConnections()
	resp, err = (&http.Client{Transport: tr2}).Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 502 || targetCalls.Load() != 1 {
		t.Fatal("failed proxy silently used a direct connection")
	}
}

func TestSOCKSProxyAuthentication(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		first := make([]byte, 2)
		if _, err = io.ReadFull(br, first); err != nil {
			done <- err
			return
		}
		methods := make([]byte, int(first[1]))
		_, err = io.ReadFull(br, methods)
		if err != nil {
			done <- err
			return
		}
		_, _ = c.Write([]byte{5, 2})
		_, _ = br.ReadByte()
		n, _ := br.ReadByte()
		user := make([]byte, n)
		_, _ = io.ReadFull(br, user)
		n, _ = br.ReadByte()
		pass := make([]byte, n)
		_, _ = io.ReadFull(br, pass)
		if string(user) != "u" || string(pass) != "p" {
			done <- io.ErrUnexpectedEOF
			return
		}
		_, _ = c.Write([]byte{1, 0})
		prefix := make([]byte, 4)
		_, _ = io.ReadFull(br, prefix)
		switch prefix[3] {
		case 1:
			_, _ = io.CopyN(io.Discard, br, 4)
		case 3:
			n, _ = br.ReadByte()
			_, _ = io.CopyN(io.Discard, br, int64(n))
		case 4:
			_, _ = io.CopyN(io.Discard, br, 16)
		}
		_, _ = io.CopyN(io.Discard, br, 2)
		_, _ = c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
		_, err = http.ReadRequest(br)
		if err == nil {
			_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 5\r\nConnection: close\r\n\r\nsocks")
		}
		done <- err
	}()
	bridge, stop, err := proxyBridge("socks5://u:p@" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	u, _ := url.Parse(bridge)
	tr := &http.Transport{Proxy: http.ProxyURL(u)}
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(context.Background(), "GET", "http://example.test/", nil)
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "socks" {
		t.Fatal("SOCKS bridge did not forward request")
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
