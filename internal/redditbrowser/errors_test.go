package redditbrowser

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBrowserOperationErrorsHideSensitiveDetails(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"page load error net::ERR_TUNNEL_CONNECTION_FAILED", "proxy connection failed"},
		{"net::ERR_PROXY_CONNECTION_FAILED", "proxy connection failed"},
		{"net::ERR_CONNECTION_RESET", "network connection failed"},
		{"net::ERR_CONNECTION_TIMED_OUT", "connection timed out"},
		{"net::ERR_CERT_AUTHORITY_INVALID", "certificate verification failed"},
		{"Execution context was destroyed", "page changed"},
		{"page load error net::ERR_ABORTED", "page changed"},
		{"websocket: close 1006", "browser connection closed"},
		{"unknown error", "browser command failed"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			cause := errors.New(tc.raw + " https://user:secret@private.example/ cookie=private-token submitted-body")
			err := browserOperationError(context.Background(), context.Background(), cause)
			if !strings.Contains(err.Error(), tc.want) || !errors.Is(err, cause) {
				t.Fatalf("unexpected classification: %v", err)
			}
			for _, private := range []string{"secret", "private", "submitted-body", "reconnect"} {
				if strings.Contains(err.Error(), private) {
					t.Fatalf("unsafe or misleading error: %v", err)
				}
			}
		})
	}
}

func TestBrowserOperationErrorsPreserveRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	err := browserOperationError(ctx, context.Background(), context.Canceled)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "request timed out") {
		t.Fatalf("deadline was masked by Chromium cancellation: %v", err)
	}
	browser, stop := context.WithCancel(context.Background())
	stop()
	err = browserOperationError(context.Background(), browser, errors.New("websocket: close private-url"))
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("browser cancellation was masked: %v", err)
	}
}

func TestNavigationFailureReportsNetworkAndDeadline(t *testing.T) {
	if Executable() == "" {
		t.Skip("Chromium not installed")
	}
	for _, stalled := range []bool{false, true} {
		t.Run(map[bool]string{false: "refused connection", true: "deadline"}[stalled], func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
			}))
			defer srv.Close()
			previous := site
			site = srv.URL
			defer func() { site = previous }()
			parent, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			s, err := Open(parent, "", false)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if !stalled {
				srv.Close()
			}
			ctx, cancel := context.WithTimeout(parent, time.Second)
			defer cancel()
			err = s.Navigate(ctx, srv.URL+"/")
			if err == nil || strings.Contains(err.Error(), srv.URL) || strings.Contains(err.Error(), "reconnect") {
				t.Fatalf("unsafe or missing navigation failure: %v", err)
			}
			if stalled {
				if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "request timed out") {
					t.Fatalf("missing request deadline: %v", err)
				}
			} else if !strings.Contains(err.Error(), "network connection failed") {
				t.Fatalf("missing network failure: %v", err)
			}
		})
	}
}
