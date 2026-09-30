package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/verrloren/radaro/internal/accountimport"
	"github.com/verrloren/radaro/internal/publish"
)

type accountImportResult struct {
	Row       int    `json:"row"`
	Platform  string `json:"platform"`
	Handle    string `json:"handle"`
	Status    string `json:"status"`
	AccountID int64  `json:"account_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

type accountImportResponse struct {
	Added   int                   `json:"added"`
	Updated int                   `json:"updated"`
	Errors  int                   `json:"errors"`
	Results []accountImportResult `json:"results"`
}

type accountImportCheck struct {
	handle string
	creds  any
	err    error
	limit  bool
}

// importAccounts checks at most three credentials at once, then saves valid
// accounts in input order. No platform error is returned because upstream
// error bodies can contain the supplied credential.
func (s *Server) importAccounts(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	rows, err := accountimport.Parse(body.Text)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	uid := userID(r)
	existing, err := s.store.Accounts(uid, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read accounts")
		return
	}
	known := make(map[string]bool, len(existing))
	for _, acc := range existing {
		known[acc.Platform+"\x00"+acc.Handle] = true
	}

	checks := make([]accountImportCheck, len(rows))
	jobs := make(chan int)
	workers := 3
	if len(rows) < workers {
		workers = len(rows)
	}
	done := make(chan struct{}, workers)
	for range workers {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := range jobs {
				row := rows[i]
				ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
				handle, creds, err := s.connect(ctx, row.Platform, publish.ConnectInput{
					Handle: row.Handle, Service: row.Instance, Instance: row.Instance, Secret: row.Secret,
				})
				checks[i] = accountImportCheck{handle: handle, creds: creds, err: err, limit: ctx.Err() != nil}
				cancel()
			}
		}()
	}
	for i, row := range rows {
		if row.Error == "" {
			jobs <- i
		}
	}
	close(jobs)
	for range workers {
		<-done
	}

	out := accountImportResponse{Results: make([]accountImportResult, 0, len(rows))}
	for i, row := range rows {
		result := accountImportResult{Row: row.Number, Platform: redactImportSecret(row.Platform, row.Secret), Handle: redactImportSecret(row.Handle, row.Secret)}
		switch {
		case row.Error != "":
			result.Error = fmt.Sprintf("row %d: %s", row.Number, row.Error)
		case checks[i].limit:
			result.Error = fmt.Sprintf("row %d: credential check timed out", row.Number)
		case checks[i].err != nil:
			result.Error = fmt.Sprintf("row %d: credential check failed; verify credentials", row.Number)
		case checks[i].handle == "":
			result.Error = fmt.Sprintf("row %d: platform returned no account handle", row.Number)
		default:
			result.Handle = redactImportSecret(checks[i].handle, row.Secret)
			key := row.Platform + "\x00" + checks[i].handle
			acc, err := s.store.SaveAccount(uid, row.Platform, checks[i].handle, checks[i].creds)
			if err != nil {
				result.Error = fmt.Sprintf("row %d: could not save account", row.Number)
			} else {
				result.AccountID = acc.ID
				if known[key] {
					result.Status = "updated"
					out.Updated++
				} else {
					result.Status = "added"
					out.Added++
					known[key] = true
				}
				_ = s.store.LogActivity(uid, "account.imported", 0, row.Platform+" "+result.Handle)
			}
		}
		if result.Error != "" {
			result.Status = "error"
			out.Errors++
		}
		out.Results = append(out.Results, result)
	}
	writeJSON(w, http.StatusOK, out)
}

func redactImportSecret(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[redacted]")
}
