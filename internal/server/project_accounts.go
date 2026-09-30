package server

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/store"
)

// poolView is a project's pool for one platform: the accounts its drafts
// publish from. Empty: drafts use any of the user's accounts on the platform.
type poolView struct {
	Platform publish.Platform `json:"platform"`
	Accounts []accountView    `json:"accounts"`
}

// projectAccounts lists every platform with the project's pool.
func (s *Server) projectAccounts(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	s.writePools(w, r, id)
}

func (s *Server) writePools(w http.ResponseWriter, r *http.Request, projectID int64) {
	bound, err := s.store.ProjectBindings(userID(r), projectID)
	if err != nil {
		storeError(w, err, http.StatusInternalServerError)
		return
	}
	out := make([]poolView, len(publish.Platforms))
	for i, p := range publish.Platforms {
		out[i] = poolView{Platform: p, Accounts: []accountView{}}
	}
	if len(bound) > 0 {
		if err := s.fillPools(userID(r), out, bound); err != nil {
			internalError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// fillPools adds each bound account, decorated, to its platform's pool.
func (s *Server) fillPools(uid int64, pools []poolView, bound []store.Binding) error {
	accs := make([]*store.Account, len(bound))
	for i, b := range bound {
		accs[i] = b.Account
	}
	views, err := s.accountViews(uid, accs)
	if err != nil {
		return err
	}
	for i, b := range bound {
		for j := range pools {
			if pools[j].Platform.Name == b.Platform {
				pools[j].Accounts = append(pools[j].Accounts, views[i])
			}
		}
	}
	return nil
}

// bindAccount adds an account to the project's pool for its platform; adding
// it again changes nothing.
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
		writeError(w, http.StatusNotFound, errAccountNotFound)
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
	s.writePools(w, r, id)
}

// unbindAccount empties the project's pool for a platform.
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
	s.writePools(w, r, id)
}

// unbindProjectAccount takes one account out of the project's pool.
func (s *Server) unbindProjectAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	aid, ok := parseID(chi.URLParam(r, "account_id"))
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, errInvalidAccount)
		return
	}
	platform := chi.URLParam(r, "platform")
	acc, err := s.store.Account(userID(r), aid)
	if err != nil {
		internalError(w, err)
		return
	}
	removed := false
	if acc != nil && acc.Platform == platform {
		if removed, err = s.store.UnbindProjectAccount(userID(r), id, aid); err != nil {
			storeError(w, err, http.StatusInternalServerError)
			return
		}
	}
	if !removed {
		writeError(w, http.StatusNotFound, "account is not in the project's "+platform+" pool")
		return
	}
	_ = s.store.LogActivity(userID(r), "account.unbound", 0, platform+" "+acc.Handle+" from project "+strconv.FormatInt(id, 10))
	s.writePools(w, r, id)
}
