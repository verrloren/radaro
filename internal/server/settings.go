package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/sources"
	"github.com/verrloren/radaro/internal/store"
)

// --- source settings ----------------------------------------------------------

type fieldView struct {
	sources.Field
	Set    bool   `json:"set"`
	Origin string `json:"origin"`          // ui | env | "" (unset)
	Value  string `json:"value,omitempty"` // non-secret fields only
}

type sourceSettingsView struct {
	Name       string      `json:"name"`
	Label      string      `json:"label"`
	Configured bool        `json:"configured"`
	Saved      bool        `json:"saved"` // has settings saved from the dashboard
	Fields     []fieldView `json:"fields"`
}

// sourceSettings describes every keyed source without ever returning a secret.
func (s *Server) sourceSettings() ([]sourceSettingsView, error) {
	stored, err := s.store.SourceSettings()
	if err != nil {
		return nil, err
	}
	merged := s.cfg.SourceOptions.Merge(stored)
	out := []sourceSettingsView{}
	for _, info := range sources.All() {
		fs := sources.Fields(info.Name)
		if len(fs) == 0 {
			continue
		}
		v := sourceSettingsView{Name: info.Name, Label: info.Label, Configured: sources.Configured(info.Name, merged), Saved: len(stored[info.Name]) > 0}
		for _, f := range fs {
			fv := fieldView{Field: f}
			switch {
			case strings.TrimSpace(stored[info.Name][f.Key]) != "":
				fv.Origin = "ui"
			case s.cfg.SourceOptions.Value(info.Name, f.Key) != "":
				fv.Origin = "env"
			}
			fv.Set = fv.Origin != ""
			if fv.Set && !f.Secret {
				fv.Value = merged.Value(info.Name, f.Key)
			}
			v.Fields = append(v.Fields, fv)
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Server) writeSourceSettings(w http.ResponseWriter, name string) {
	all, err := s.sourceSettings()
	if err != nil {
		internalError(w, err)
		return
	}
	for _, v := range all {
		if v.Name == name {
			writeJSON(w, http.StatusOK, v)
			return
		}
	}
	writeError(w, http.StatusNotFound, "source has no settings")
}

func (s *Server) listSourceSettings(w http.ResponseWriter, _ *http.Request) {
	all, err := s.sourceSettings()
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, all)
}

// saveSourceSettings replaces a source's saved settings. A blank secret keeps
// the one saved before, so the form never needs to echo secrets back.
func (s *Server) saveSourceSettings(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	fs := sources.Fields(name)
	if len(fs) == 0 {
		writeError(w, http.StatusNotFound, "source has no settings")
		return
	}
	var body struct {
		Values map[string]string `json:"values"`
	}
	if !decode(w, r, &body) {
		return
	}
	known := map[string]bool{}
	for _, f := range fs {
		known[f.Key] = true
	}
	for k := range body.Values {
		if !known[k] {
			writeError(w, http.StatusUnprocessableEntity, "unknown field: "+k)
			return
		}
	}
	stored, err := s.store.SourceSettings()
	if err != nil {
		internalError(w, err)
		return
	}
	values := map[string]string{}
	for _, f := range fs {
		v := strings.TrimSpace(body.Values[f.Key])
		if f.Key == "feeds" {
			v = strings.Join(sources.SplitFeeds(v), "\n")
		}
		if v == "" && f.Secret {
			v = stored[name][f.Key]
		}
		if v != "" {
			values[f.Key] = v
		}
	}
	if len(values) == 0 {
		_, err = s.store.DeleteSourceSettings(name)
	} else {
		err = s.store.SaveSourceSettings(name, values)
	}
	if err != nil {
		internalError(w, err)
		return
	}
	_ = s.store.LogActivity(userID(r), "source.configured", 0, name)
	s.writeSourceSettings(w, name)
}

func (s *Server) deleteSourceSettings(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if len(sources.Fields(name)) == 0 {
		writeError(w, http.StatusNotFound, "source has no settings")
		return
	}
	deleted, err := s.store.DeleteSourceSettings(name)
	if err != nil {
		internalError(w, err)
		return
	}
	if deleted {
		_ = s.store.LogActivity(userID(r), "source.reset", 0, name)
	}
	s.writeSourceSettings(w, name)
}

// --- publishing accounts ------------------------------------------------------

func (s *Server) accounts(w http.ResponseWriter, r *http.Request) {
	accs, err := s.store.Accounts(userID(r), "")
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"platforms": publish.Platforms, "accounts": accs})
}

func (s *Server) connectAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Platform  string `json:"platform"`
		Handle    string `json:"handle"`
		Service   string `json:"service"`
		Instance  string `json:"instance"`
		Secret    string `json:"secret"`
		ProjectID int64  `json:"project_id"` // optional: bind to this project right away
	}
	if !decode(w, r, &body) {
		return
	}
	if _, ok := publish.LookupPlatform(body.Platform); !ok {
		writeError(w, http.StatusUnprocessableEntity, "unknown platform: "+body.Platform)
		return
	}
	if body.ProjectID != 0 && !s.ownProject(w, r, body.ProjectID) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	handle, creds, err := s.connect(ctx, body.Platform, publish.ConnectInput{
		Handle: body.Handle, Service: body.Service, Instance: body.Instance, Secret: body.Secret,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	acc, err := s.store.SaveAccount(userID(r), body.Platform, handle, creds)
	if err != nil {
		internalError(w, err)
		return
	}
	_ = s.store.LogActivity(userID(r), "account.connected", 0, body.Platform+" "+handle)
	if body.ProjectID != 0 {
		if _, err := s.store.BindAccount(userID(r), body.ProjectID, acc.ID); err != nil {
			storeError(w, err, http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, http.StatusCreated, acc)
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "invalid account id")
		return
	}
	deleted, err := s.store.DeleteAccount(userID(r), id)
	if err != nil {
		internalError(w, err)
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	_ = s.store.LogActivity(userID(r), "account.removed", 0, "account "+strconv.FormatInt(id, 10))
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// --- Reddit OAuth ---------------------------------------------------------------

const redditCallbackPath = "/oauth/reddit/callback"

// oauthStates holds Reddit sign-ins in flight: state → the app credentials,
// single use and short-lived.
type oauthStates struct {
	mu      sync.Mutex
	pending map[string]pendingOAuth
}

type pendingOAuth struct {
	userID    int64 // the callback carries no session: the state says whose account it is
	projectID int64 // bind the account here once connected; 0 = none
	creds     publish.RedditCredentials
	expires   time.Time
}

func (o *oauthStates) put(state string, p pendingOAuth) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.pending == nil {
		o.pending = map[string]pendingOAuth{}
	}
	now := time.Now()
	for k, v := range o.pending {
		if now.After(v.expires) {
			delete(o.pending, k)
		}
	}
	o.pending[state] = p
}

func (o *oauthStates) take(state string) (pendingOAuth, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	p, ok := o.pending[state]
	delete(o.pending, state)
	if !ok || time.Now().After(p.expires) {
		return pendingOAuth{}, false
	}
	return p, true
}

// redditRedirectURI is the callback on the address the dashboard was opened
// at; it must match the redirect URI registered in the user's Reddit app.
func redditRedirectURI(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host + redditCallbackPath
}

func (s *Server) redditAuthorize(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		ProjectID    int64  `json:"project_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.ProjectID != 0 && !s.ownProject(w, r, body.ProjectID) {
		return
	}
	body.ClientID = strings.TrimSpace(body.ClientID)
	if body.ClientID == "" {
		writeError(w, http.StatusUnprocessableEntity, "client_id is required")
		return
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		internalError(w, err)
		return
	}
	state := hex.EncodeToString(buf)
	redirect := redditRedirectURI(r)
	s.oauth.put(state, pendingOAuth{
		userID:    userID(r),
		projectID: body.ProjectID,
		creds:     publish.RedditCredentials{ClientID: body.ClientID, ClientSecret: strings.TrimSpace(body.ClientSecret), RedirectURI: redirect},
		expires:   time.Now().Add(10 * time.Minute),
	})
	writeJSON(w, http.StatusOK, map[string]string{
		"authorize_url": publish.RedditAuthorizeURL(body.ClientID, redirect, state),
		"redirect_uri":  redirect,
	})
}

// redditCallback finishes the sign-in and sends the browser back to Setup.
func (s *Server) redditCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	back := func(key, value string) {
		http.Redirect(w, r, "/?v=setup&"+key+"="+url.QueryEscape(value), http.StatusSeeOther)
	}
	p, ok := s.oauth.take(q.Get("state"))
	if !ok {
		back("connect_error", "The Reddit sign-in expired or did not start here. Try again.")
		return
	}
	if e := q.Get("error"); e != "" {
		back("connect_error", "Reddit refused access: "+e)
		return
	}
	code := q.Get("code")
	if code == "" {
		back("connect_error", "Reddit did not return an authorization code.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rd := &publish.Reddit{Version: s.version, Creds: p.creds}
	if err := s.redditExchange(ctx, rd, code); err != nil {
		back("connect_error", "Reddit sign-in failed: "+err.Error())
		return
	}
	acc, err := s.store.SaveAccount(p.userID, "reddit", rd.Creds.Username, rd.Creds)
	if err != nil {
		back("connect_error", "Could not save the account: "+err.Error())
		return
	}
	if p.projectID != 0 {
		if _, err := s.store.BindAccount(p.userID, p.projectID, acc.ID); err != nil {
			back("connect_error", "Connected, but could not add the account to the project: "+err.Error())
			return
		}
	}
	_ = s.store.LogActivity(p.userID, "account.connected", 0, "reddit "+rd.Creds.Username)
	back("connected", "reddit")
}

// --- project accounts -----------------------------------------------------------

type bindingView struct {
	Platform publish.Platform `json:"platform"`
	Account  *store.Account   `json:"account"` // null: nothing bound
}

// projectAccounts lists every platform with the project's account, if any.
func (s *Server) projectAccounts(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	s.writeBindings(w, r, id)
}

func (s *Server) writeBindings(w http.ResponseWriter, r *http.Request, projectID int64) {
	bound, err := s.store.ProjectBindings(userID(r), projectID)
	if err != nil {
		storeError(w, err, http.StatusInternalServerError)
		return
	}
	out := make([]bindingView, len(publish.Platforms))
	for i, p := range publish.Platforms {
		out[i].Platform = p
		for _, b := range bound {
			if b.Platform == p.Name {
				out[i].Account = b.Account
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) bindAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok || !s.ownProject(w, r, id) {
		return
	}
	platform := chi.URLParam(r, "platform")
	var body struct {
		AccountID int64 `json:"account_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	acc, err := s.store.Account(userID(r), body.AccountID)
	if err != nil {
		internalError(w, err)
		return
	}
	if acc == nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	if acc.Platform != platform {
		writeError(w, http.StatusUnprocessableEntity, "account "+strconv.FormatInt(acc.ID, 10)+" is not a "+platform+" account")
		return
	}
	if _, err := s.store.BindAccount(userID(r), id, acc.ID); err != nil {
		storeError(w, err, http.StatusInternalServerError)
		return
	}
	_ = s.store.LogActivity(userID(r), "account.bound", 0, platform+" "+acc.Handle+" to project "+strconv.FormatInt(id, 10))
	s.writeBindings(w, r, id)
}

func (s *Server) unbindAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	platform := chi.URLParam(r, "platform")
	if _, err := s.store.UnbindAccount(userID(r), id, platform); err != nil {
		storeError(w, err, http.StatusInternalServerError)
		return
	}
	_ = s.store.LogActivity(userID(r), "account.unbound", 0, platform+" from project "+strconv.FormatInt(id, 10))
	s.writeBindings(w, r, id)
}
