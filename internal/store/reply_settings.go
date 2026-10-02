package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type ReplySettings struct {
	Brief        string `json:"brief"`
	Instructions string `json:"instructions"`
	Language     string `json:"language"`
	Tone         string `json:"tone"`
}

func (s *Store) ReplySettings(userID, projectID int64) (ReplySettings, error) {
	if _, err := ownedProject(s.rdb, userID, projectID); err != nil {
		return ReplySettings{}, err
	}
	var raw string
	err := s.rdb.QueryRow(`SELECT settings FROM project_reply_settings WHERE project_id=?`, projectID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ReplySettings{Language: "Same as the post", Tone: "Helpful and concise"}, nil
	}
	if err != nil {
		return ReplySettings{}, err
	}
	var out ReplySettings
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}

func (s *Store) SaveReplySettings(userID, projectID int64, settings ReplySettings) error {
	if _, err := ownedProject(s.db, userID, projectID); err != nil {
		return err
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO project_reply_settings(project_id,settings,updated_at) VALUES(?,?,?)
	ON CONFLICT(project_id) DO UPDATE SET settings=excluded.settings,updated_at=excluded.updated_at`, projectID, string(raw), stamp(time.Now()))
	return err
}

// A click after a lost response opens the same draft, including one already sent.
func (s *Store) ReplyDraft(userID, projectID int64, mentionID string) (*Draft, error) {
	where, args := owner("user_id", userID)
	d, err := scanDraft(s.rdb.QueryRow(`SELECT `+draftColumns+` FROM drafts WHERE project_id=? AND mention_id=? AND platform='reddit' AND kind='reply' AND status!='skipped' AND `+where+` ORDER BY id DESC LIMIT 1`, append([]any{projectID, mentionID}, args...)...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

func (s *Store) ProjectForQuery(userID int64, query string) (int64, error) {
	var id int64
	where, args := owner("p.user_id", userID)
	err := s.rdb.QueryRow(`SELECT p.id FROM projects p JOIN project_keywords k ON k.project_id=p.id WHERE k.query=? AND `+where+` ORDER BY p.is_default DESC,p.id LIMIT 1`, append([]any{query}, args...)...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}
