package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ProjectRow is one registered project folder. The registry in
// internal/project owns validation; this layer only persists.
type ProjectRow struct {
	ID          string
	Path        string
	DisplayName string
	CreatedAt   time.Time
	LastUsedAt  time.Time
}

// InsertProject stores a new project. The path is UNIQUE, so registering an
// already-registered directory returns an error — callers look it up by path
// first (see project.Registry.Register).
func (s *Store) InsertProject(ctx context.Context, p ProjectRow) error {
	if s == nil || s.db == nil {
		return unavailable(errors.New("store not open"))
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO projects (id, path, display_name, created_at, last_used_at)
		VALUES (?, ?, ?, ?, ?)`,
		p.ID, p.Path, p.DisplayName, p.CreatedAt.Unix(), p.LastUsedAt.Unix())
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func scanProject(scan func(dest ...any) error) (ProjectRow, error) {
	var (
		p                     ProjectRow
		createdAt, lastUsedAt int64
	)
	if err := scan(&p.ID, &p.Path, &p.DisplayName, &createdAt, &lastUsedAt); err != nil {
		return ProjectRow{}, err
	}
	p.CreatedAt = time.Unix(createdAt, 0).UTC()
	p.LastUsedAt = time.Unix(lastUsedAt, 0).UTC()
	return p, nil
}

const projectColumns = `id, path, display_name, created_at, last_used_at`

// GetProject returns the project with this id.
func (s *Store) GetProject(ctx context.Context, id string) (ProjectRow, bool, error) {
	if s == nil || s.db == nil {
		return ProjectRow{}, false, unavailable(errors.New("store not open"))
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE id = ?`, id)

	p, err := scanProject(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectRow{}, false, nil
	}
	if err != nil {
		return ProjectRow{}, false, unavailable(err)
	}
	return p, true, nil
}

// GetProjectByPath returns the project registered at this resolved path.
// Registration is idempotent because of this lookup.
func (s *Store) GetProjectByPath(ctx context.Context, path string) (ProjectRow, bool, error) {
	if s == nil || s.db == nil {
		return ProjectRow{}, false, unavailable(errors.New("store not open"))
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE path = ?`, path)

	p, err := scanProject(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectRow{}, false, nil
	}
	if err != nil {
		return ProjectRow{}, false, unavailable(err)
	}
	return p, true, nil
}

// ListProjects returns every project, most recently used first.
func (s *Store) ListProjects(ctx context.Context) ([]ProjectRow, error) {
	if s == nil || s.db == nil {
		return nil, unavailable(errors.New("store not open"))
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+projectColumns+` FROM projects ORDER BY last_used_at DESC`)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []ProjectRow
	for rows.Next() {
		p, err := scanProject(rows.Scan)
		if err != nil {
			return nil, unavailable(err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// TouchProject records that the project was used at time at.
func (s *Store) TouchProject(ctx context.Context, id string, at time.Time) error {
	if s == nil || s.db == nil {
		return unavailable(errors.New("store not open"))
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE projects SET last_used_at = ? WHERE id = ?`, at.Unix(), id)
	if err != nil {
		return unavailable(err)
	}
	return nil
}
