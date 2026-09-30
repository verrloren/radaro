package server

import (
	"net/http"
	"os"
	"strings"

	"github.com/verrloren/radaro/internal/netproxy"
)

type proxySettingsView struct {
	Configured bool   `json:"configured"`
	Origin     string `json:"origin"` // ui | env | empty
}

// proxyView never returns a stored URL: it may include a proxy password.
func (s *Server) proxyView() (proxySettingsView, error) {
	raw, err := s.store.ProxyURL()
	if err != nil {
		return proxySettingsView{}, err
	}
	if raw != "" {
		return proxySettingsView{Configured: true, Origin: "ui"}, nil
	}
	if os.Getenv("HTTPS_PROXY") != "" || os.Getenv("https_proxy") != "" ||
		os.Getenv("HTTP_PROXY") != "" || os.Getenv("http_proxy") != "" {
		return proxySettingsView{Configured: true, Origin: "env"}, nil
	}
	return proxySettingsView{}, nil
}

func (s *Server) proxySettings(w http.ResponseWriter, _ *http.Request) {
	v, err := s.proxyView()
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) saveProxySettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &body) {
		return
	}
	raw := strings.TrimSpace(body.URL)
	if raw == "" {
		writeError(w, http.StatusUnprocessableEntity, "proxy url is required; use DELETE to restore the environment")
		return
	}
	if err := netproxy.Validate(raw); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := s.store.SaveProxyURL(raw); err != nil {
		internalError(w, err)
		return
	}
	if err := netproxy.Set(raw); err != nil {
		internalError(w, err)
		return
	}
	_ = s.store.LogActivity(userID(r), "proxy.configured", 0, "outbound proxy configured")
	writeJSON(w, http.StatusOK, proxySettingsView{Configured: true, Origin: "ui"})
}

func (s *Server) deleteProxySettings(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SaveProxyURL(""); err != nil {
		internalError(w, err)
		return
	}
	_ = netproxy.Set("")
	_ = s.store.LogActivity(userID(r), "proxy.reset", 0, "outbound proxy reset")
	s.proxySettings(w, r)
}
