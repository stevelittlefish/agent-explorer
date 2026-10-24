package main

import (
	"bytes"
	"database/sql"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the cgo-free "sqlite" driver
)

// The impd daemon keeps its sessions in SQLite rather than JSONL: one session
// row each (working directory, title) and one message row per entry, holding
// the entry's JSON exactly as imp writes it to a JSONL line. So daemon sessions
// are read by the imp parser, listed with imp's own, and export as JSONL.

func (s *Store) impdDB() (*sql.DB, error) {
	s.dbMu.Lock()
	defer s.dbMu.Unlock()
	if s.impd == nil {
		path := filepath.ToSlash(s.Impd)
		if !strings.HasPrefix(path, "/") {
			path = "/" + path // file:///C:/... on Windows
		}
		u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
		db, err := sql.Open("sqlite", u.String())
		if err != nil {
			return nil, err
		}
		s.impd = db
	}
	return s.impd, nil
}

func (s *Store) closeImpd() {
	s.dbMu.Lock()
	defer s.dbMu.Unlock()
	if s.impd != nil {
		s.impd.Close()
		s.impd = nil
	}
}

func impdKey(db, id string) string { return db + "#" + id }

// listImpd adds the daemon's sessions to chats. Called with s.mu held. A
// database that can't be read is logged and skipped, so imp's JSONL sessions
// still show.
func (s *Store) listImpd(chats *[]Chat, seen map[string]bool) {
	if s.Impd == "" {
		return
	}
	if _, err := os.Stat(s.Impd); err != nil {
		return
	}
	db, err := s.impdDB()
	if err == nil {
		err = s.scanImpd(db, chats, seen)
	}
	if err != nil {
		log.Printf("read impd sessions in %s: %v", s.Impd, err)
	}
}

func (s *Store) scanImpd(db *sql.DB, chats *[]Chat, seen map[string]bool) error {
	rows, err := db.Query(`SELECT s.id, s.cwd, s.title, s.created_at, s.updated_at, MAX(m.seq)
		FROM session s JOIN message m ON m.session_id = s.id GROUP BY s.id`)
	if err != nil {
		return err
	}
	type info struct {
		id, cwd, title string
		updated        time.Time
		last           int64
	}
	var sessions []info
	for rows.Next() {
		var i info
		var created, updated string
		if err := rows.Scan(&i.id, &i.cwd, &i.title, &created, &updated, &i.last); err != nil {
			rows.Close()
			return err
		}
		if updated == "" {
			updated = created
		}
		i.updated, _ = time.Parse(time.RFC3339Nano, updated)
		sessions = append(sessions, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, i := range sessions {
		k := impdKey(s.Impd, i.id)
		cached, ok := s.cache[k]
		// New entries raise the last seq; a rename changes only the title.
		if !ok || cached.size != i.last || !cached.modified.Equal(i.updated) || cached.title != i.title {
			chat, err := readImpd(db, s.Impd, i.id, i.cwd, i.title, false)
			if err != nil {
				return err
			}
			chat.Updated = i.updated
			cached = cachedChat{size: i.last, modified: i.updated, title: i.title, chat: chat}
			s.cache[k] = cached
		}
		seen[k] = true
		*chats = append(*chats, cached.chat)
	}
	return nil
}

func impdLines(db *sql.DB, id string) ([]byte, error) {
	rows, err := db.Query(`SELECT data FROM message WHERE session_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var b bytes.Buffer
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		b.Write(bytes.TrimSpace(data))
		b.WriteByte('\n')
	}
	return b.Bytes(), rows.Err()
}

func readImpd(db *sql.DB, path, id, cwd, title string, full bool) (Chat, error) {
	c := Chat{ID: key(impdKey(path, id)), Path: path, Session: id, Project: cwd, Title: id}
	if c.Project == "" {
		c.Project = "Unknown project"
	}
	lines, err := impdLines(db, id)
	if err != nil {
		return c, err
	}
	c, err = readRecords(c, bytes.NewReader(lines), "imp", full)
	if title != "" {
		c.Title = title
	}
	return c, err
}

// Read reads a listed chat again in full.
func (s *Store) Read(c Chat, agent string) (Chat, error) {
	if c.Session == "" {
		return readChat(c.Path, agent, true)
	}
	db, err := s.impdDB()
	if err != nil {
		return c, err
	}
	var cwd, title string
	if err := db.QueryRow(`SELECT cwd, title FROM session WHERE id = ?`, c.Session).Scan(&cwd, &title); err != nil {
		return c, err
	}
	return readImpd(db, c.Path, c.Session, cwd, title, true)
}

// Original returns an impd chat's entries as JSON lines, as imp would write them.
func (s *Store) Original(c Chat) ([]byte, error) {
	db, err := s.impdDB()
	if err != nil {
		return nil, err
	}
	return impdLines(db, c.Session)
}
