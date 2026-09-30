package netproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetAppliesToNewRequestsAndCanBeCleared(t *testing.T) {
	defer Set("")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "direct")
	}))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() {
			t.Errorf("proxy received a relative URL: %s", r.URL)
		}
		_, _ = io.WriteString(w, "via proxy")
	}))
	defer proxy.Close()

	client := &http.Client{Transport: Transport()}
	read := func() string {
		t.Helper()
		res, err := client.Get(target.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	if err := Set(proxy.URL); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "via proxy" {
		t.Fatalf("configured proxy: %q", got)
	}
	if err := Set(""); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "direct" {
		t.Fatalf("after reset: %q", got)
	}
}

func TestValidationDoesNotRevealCredentials(t *testing.T) {
	for _, raw := range []string{"http://user:private@localhost:bad", "ftp://user:private@localhost:1234", "http://user:private@localhost"} {
		err := Validate(raw)
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatalf("invalid proxy URL was accepted or exposed credentials")
		}
	}
}
