package sqlite

import (
	"context"
	"database/sql"
)

type Store struct {
	Driver string
	Path   string
	DB     *sql.DB
}

func New(path string) *Store {
	return &Store{
		Driver: "sqlite",
		Path:   path,
	}
}

func NewWithDriver(driver, path string) *Store {
	return &Store{
		Driver: driver,
		Path:   path,
	}
}

func (s *Store) Attach(db *sql.DB) {
	s.DB = db
}

func (s *Store) DBTX() DBTX {
	return s.DB
}

func (s *Store) Open() error {
	db, err := Open(s.Driver, s.Path)
	if err != nil {
		return err
	}
	s.DB = db
	return nil
}

func (s *Store) Close() error {
	if s.DB == nil {
		return nil
	}
	err := s.DB.Close()
	s.DB = nil
	return err
}

func (s *Store) Migrate(ctx context.Context) error {
	return Migrate(ctx, s.DB)
}

func (s *Store) Ping(ctx context.Context) error {
	return Ping(ctx, s.DB)
}
