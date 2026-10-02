package redditbrowser

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

var browserNetworkCode = regexp.MustCompile(`^page load error net::(ERR_[A-Z_]+)\b`)
var errNetworkChanged = errors.New("Reddit browser network changed during page load")

// Chromium errors can contain URLs, proxy credentials or submitted text.
// Only fixed descriptions and known network codes may reach callers or logs.
type operationError struct {
	message string
	cause   error
	kind    error
}

func (e *operationError) Error() string        { return e.message }
func (e *operationError) Unwrap() error        { return e.cause }
func (e *operationError) Is(target error) bool { return e.kind != nil && target == e.kind }

func browserOperationError(ctx, browser context.Context, err error) error {
	if ctx.Err() != nil {
		err = ctx.Err()
	} else if browser.Err() != nil {
		err = browser.Err()
	}
	message := "Reddit browser command failed; try loading again"
	var kind error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		message = "Reddit browser request timed out; the page or proxy did not respond in time"
	case errors.Is(err, context.Canceled):
		message = "Reddit browser request was interrupted; try loading again"
	default:
		raw := err.Error()
		switch {
		case browserNetworkCode.FindString(raw) == "page load error net::ERR_NETWORK_CHANGED":
			message = "Reddit browser network changed during page load; try loading again"
			kind = errNetworkChanged
		case strings.Contains(raw, "ERR_PROXY_CONNECTION_FAILED"), strings.Contains(raw, "ERR_TUNNEL_CONNECTION_FAILED"), strings.Contains(raw, "ERR_NO_SUPPORTED_PROXIES"):
			message = "Reddit proxy connection failed; check the account proxy"
		case strings.Contains(raw, "ERR_TIMED_OUT"), strings.Contains(raw, "ERR_CONNECTION_TIMED_OUT"):
			message = "Reddit connection timed out; check the connection or account proxy"
		case strings.Contains(raw, "ERR_CONNECTION_RESET"), strings.Contains(raw, "ERR_CONNECTION_CLOSED"), strings.Contains(raw, "ERR_CONNECTION_REFUSED"), strings.Contains(raw, "ERR_NAME_NOT_RESOLVED"), strings.Contains(raw, "ERR_INTERNET_DISCONNECTED"):
			message = "Reddit network connection failed; check the connection or account proxy"
		case strings.Contains(raw, "ERR_CERT_"):
			message = "Reddit TLS certificate verification failed; check the account proxy"
		case strings.Contains(raw, "ERR_ABORTED"), strings.Contains(raw, "Execution context was destroyed"), strings.Contains(raw, "Cannot find context with specified id"):
			message = "Reddit page changed during the browser operation; try loading again"
		case strings.Contains(raw, "websocket: close"), strings.Contains(raw, "target closed"), strings.Contains(raw, "could not dial"), strings.Contains(raw, "inspector.detached"):
			message = "Reddit browser connection closed; try loading again"
		default:
			if code := browserNetworkCode.FindStringSubmatch(raw); len(code) == 2 {
				message = "Reddit page load failed (" + code[1] + "); check the connection or account proxy"
			}
		}
	}
	return &operationError{message: message, cause: err, kind: kind}
}
