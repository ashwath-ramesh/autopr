package db

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// Store provides read/write access to the SQLite database.
// Writer has MaxOpenConns(1); Reader has MaxOpenConns(4) and is read-only.
type Store struct {
	Writer *sql.DB
	Reader *sql.DB
	path   string
}

func Open(path string) (*Store, error) {
	writerDSN := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_foreign_keys=ON", path)
	w, err := sql.Open("sqlite3", writerDSN)
	if err != nil {
		return nil, fmt.Errorf("open writer db: %w", err)
	}
	w.SetMaxOpenConns(1)

	readerDSN := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON&mode=ro", path)
	r, err := sql.Open("sqlite3", readerDSN)
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("open reader db: %w", err)
	}
	r.SetMaxOpenConns(4)

	s := &Store{Writer: w, Reader: r, path: path}
	if err := s.createSchema(); err != nil {
		_ = w.Close()
		_ = r.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	e1 := s.Reader.Close()
	e2 := s.Writer.Close()
	if e1 != nil {
		return e1
	}
	return e2
}
