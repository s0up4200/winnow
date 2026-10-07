package winnow

import (
	"database/sql"
	embedfs "embed"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path"
	"slices"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embedfs.FS

// store is the SQLite database of the Digests. It holds the matched Events,
// the first-run time of each Digest, and the send state of each Period.
type store struct {
	db *sql.DB
}

// openStore opens the store at path and applies the pending migrations.
// When the file is missing, it creates the file and logs the path. A file
// that does not open or does not pass PRAGMA quick_check is an error,
// because an empty store would send old Digests again.
func openStore(path string, log *slog.Logger) (*store, error) {
	_, err := os.Stat(path)
	created := errors.Is(err, fs.ErrNotExist)
	q := url.Values{"_pragma": {"journal_mode(WAL)", "synchronous(NORMAL)", "busy_timeout(10000)", "foreign_keys(ON)"}}
	// The URL escapes "?", "#", and "%" in path, which SQLite reads as URI
	// syntax. OmitHost writes a relative path as file:winnow.db, not as
	// file://winnow.db.
	uri := url.URL{Scheme: "file", OmitHost: true, Path: path, RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	s := &store{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store %s: %w", path, err)
	}
	if created {
		log.Info("store created", "path", path)
	}
	return s, nil
}

// init checks the database and applies the pending migrations in one
// transaction.
func (s *store) init() error {
	var check string
	if err := s.db.QueryRow("PRAGMA quick_check").Scan(&check); err != nil {
		return err
	}
	if check != "ok" {
		return fmt.Errorf("quick_check: %s", check)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // a no-op after Commit
	if _, err := tx.Exec("CREATE TABLE IF NOT EXISTS migrations (filename TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)"); err != nil {
		return err
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	rows, err := tx.Query("SELECT filename FROM migrations")
	if err != nil {
		return err
	}
	applied := map[string]bool{}
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return err
		}
		applied[f] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for f := range applied {
		if !slices.Contains(files, path.Join("migrations", f)) {
			return fmt.Errorf("migration %s is not in this version of winnow", f)
		}
	}
	for _, f := range files { // fs.Glob sorts the names
		name := path.Base(f)
		if applied[name] {
			continue
		}
		data, err := migrations.ReadFile(f)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(data)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec("INSERT INTO migrations (filename, applied_at) VALUES (?, ?)", name, time.Now().Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *store) close() error { return s.db.Close() }

// addEvent stores e with the receive time at, for the Digests named in
// digests. It does not store a second Event with the same delivery ID, for
// example a redelivery.
func (s *store) addEvent(e *Event, at time.Time, digests []string) error {
	c := *e
	c.Body, c.Push.Commits = "", nil
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // a no-op after Commit
	var id int64
	err = tx.QueryRow("INSERT OR IGNORE INTO events (received_at, delivery, repo, name, event) VALUES (?, NULLIF(?, ''), ?, ?, ?) RETURNING id",
		at.Unix(), e.Delivery, e.Repo, e.Name, data).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) { // a redelivery
		return nil
	}
	if err != nil {
		return err
	}
	for _, d := range digests {
		if _, err := tx.Exec("INSERT INTO digest_events (digest, event) VALUES (?, ?)", d, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// events returns the stored Events of the Digest name that winnow received
// in [from, to), in the order that winnow received them.
func (s *store) events(name string, from, to time.Time) ([]Event, error) {
	rows, err := s.db.Query(`SELECT e.event FROM events e JOIN digest_events d ON d.event = e.id
		WHERE d.digest = ? AND e.received_at >= ? AND e.received_at < ? ORDER BY e.received_at, e.id`, name, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var es []Event
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var e Event
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, err
		}
		es = append(es, e)
	}
	return es, rows.Err()
}

// firstRun returns the first-run time of the Digest key. When the Digest
// has none, firstRun sets it to now.
func (s *store) firstRun(key string, now time.Time) (time.Time, error) {
	if _, err := s.db.Exec("INSERT OR IGNORE INTO digests (name, first_run) VALUES (?, ?)", key, now.Unix()); err != nil {
		return time.Time{}, err
	}
	var at int64
	err := s.db.QueryRow("SELECT first_run FROM digests WHERE name = ?", key).Scan(&at)
	return time.Unix(at, 0).In(now.Location()), err
}

// state returns the send state of the Period of the Digest key, or "" when
// the Period has no state.
func (s *store) state(key, period string) (string, error) {
	var state string
	err := s.db.QueryRow("SELECT state FROM periods WHERE digest = ? AND period = ?", key, period).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return state, err
}

// setState sets the send state of the Period of the Digest key.
func (s *store) setState(key, period, state string) error {
	_, err := s.db.Exec("INSERT OR REPLACE INTO periods (digest, period, state) VALUES (?, ?, ?)", key, period, state)
	return err
}
