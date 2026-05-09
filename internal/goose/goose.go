// Package goose provides database migration functionality.
// It is a fork of pressly/goose with additional features and bug fixes.
package goose

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Direction represents the direction of a migration.
type Direction string

const (
	// DirectionUp applies a migration.
	DirectionUp Direction = "up"
	// DirectionDown rolls back a migration.
	DirectionDown Direction = "down"
)

// MigrationType represents the type of migration file.
type MigrationType string

const (
	// TypeSQL represents a SQL migration file.
	TypeSQL MigrationType = "sql"
	// TypeGo represents a Go migration file.
	TypeGo MigrationType = "go"
)

// Migration represents a single database migration.
type Migration struct {
	// Version is the numeric version of the migration derived from the filename.
	Version int64
	// Next is the version of the next migration, or -1 if this is the last.
	Next int64
	// Previous is the version of the previous migration, or -1 if this is the first.
	Previous int64
	// Source is the path to the migration file.
	Source string
	// Registered indicates whether the migration was registered via Go code.
	Registered bool
	// Type is the type of the migration file.
	Type MigrationType
	// UpFn is the function to run when applying the migration (Go migrations only).
	UpFn func(ctx context.Context, tx *sql.Tx) error
	// DownFn is the function to run when rolling back the migration (Go migrations only).
	DownFn func(ctx context.Context, tx *sql.Tx) error
}

// MigrationRecord represents a record in the migration history table.
type MigrationRecord struct {
	VersionID int64
	TStamp    time.Time
	IsApplied bool
}

// ErrNoMigrations is returned when no migrations are found.
var ErrNoMigrations = errors.New("no migrations found")

// ErrAlreadyApplied is returned when a migration has already been applied.
var ErrAlreadyApplied = errors.New("migration already applied")

// ErrVersionNotFound is returned when a specific migration version cannot be found.
var ErrVersionNotFound = errors.New("migration version not found")

// parseMigrationVersion extracts the version number from a migration filename.
// Filenames are expected to follow the pattern: {version}_{name}.{sql|go}
func parseMigrationVersion(filename string) (int64, error) {
	base := filepath.Base(filename)
	parts := strings.SplitN(base, "_", 2)
	if len(parts) < 2 {
		return 0, fmt.Errorf("invalid migration filename: %q, expected format: {version}_{name}.{sql|go}", base)
	}
	v, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse version from filename %q: %w", base, err)
	}
	if v <= 0 {
		return 0, fmt.Errorf("migration version must be greater than 0, got %d in %q", v, base)
	}
	return v, nil
}

// collectMigrations scans the provided filesystem for migration files and
// returns them sorted by version in ascending order.
func collectMigrations(fsys fs.FS, dir string) ([]*Migration, error) {
	var migrations []*Migration

	err := fs.WalkDir(fsys, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != dir {
			return fs.SkipDir
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".sql" && ext != ".go" {
			return nil
		}
		version, err := parseMigrationVersion(path)
		if err != nil {
			return err
		}
		var mType MigrationType
		if ext == ".sql" {
			mType = TypeSQL
		} else {
			mType = TypeGo
		}
		migrations = append(migrations, &Migration{
			Version:  version,
			Next:     -1,
			Previous: -1,
			Source:   path,
			Type:     mType,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking migration directory %q: %w", dir, err)
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	// Link migrations together.
	for i, m := range migrations {
		if i > 0 {
			m.Previous = migrations[i-1].Version
			migrations[i-1].Next = m.Version
		}
	}

	return migrations, nil
}
