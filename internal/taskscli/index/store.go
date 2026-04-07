package index

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	currentSchemaVersion = 2
)

// Store manages the SQLite cache.
type Store struct {
	db *sql.DB
}

// TaskRow mirrors the tasks table schema.
type TaskRow struct {
	ID          string
	Title       string
	Status      string
	Priority    string
	Type        string
	Tags        string
	Project     string
	Context     string
	ParentID    string
	SprintID    string
	CreatedAt   string
	UpdatedAt   string
	IterationID string
	FilePath    string
}

// Filters define query filters for listing tasks.
type Filters struct {
	Statuses  []string
	Types     []string
	Tags      []string
	Priority  []string
	SprintIDs []string
	Limit     int
}

// Open creates directories, opens the SQLite DB, and runs migrations.
func Open(ctx context.Context, dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}

	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_fk=1", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}

	return s, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_version (
    version     INTEGER PRIMARY KEY,
    applied_at  TEXT NOT NULL
)`); err != nil {
		return err
	}

	var version int
	err = tx.QueryRowContext(ctx, `SELECT version FROM schema_version ORDER BY version DESC LIMIT 1`).Scan(&version)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == sql.ErrNoRows {
		version = 0
	}

	if version > currentSchemaVersion {
		return fmt.Errorf("db schema version %d is newer than supported %d", version, currentSchemaVersion)
	}

	for version < currentSchemaVersion {
		next := version + 1
		switch next {
		case 1:
			if _, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS tasks (
    id           TEXT PRIMARY KEY,
    title        TEXT NOT NULL,
    status       TEXT NOT NULL,
    priority     TEXT,
    type         TEXT,
    tags         TEXT,
    project      TEXT,
    context      TEXT,
    parent_id    TEXT,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    iteration_id TEXT,
    file_path    TEXT NOT NULL
)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_tasks_type ON tasks(type)`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_tasks_tags ON tasks(tags)`); err != nil {
				return err
			}
		case 2:
			if _, err = tx.ExecContext(ctx, `ALTER TABLE tasks ADD COLUMN sprint_id TEXT`); err != nil {
				// Ignore duplicate column errors (brand-new DBs already have the column after CREATE TABLE)
				if !strings.Contains(err.Error(), "duplicate column") {
					return err
				}
			}
		}

		version = next
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`, version, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// UpsertTask creates or updates a row.
func (s *Store) UpsertTask(ctx context.Context, row TaskRow) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO tasks (
    id, title, status, priority, type, tags, project, context, parent_id, sprint_id, created_at, updated_at, iteration_id, file_path
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    title=excluded.title,
    status=excluded.status,
    priority=excluded.priority,
    type=excluded.type,
    tags=excluded.tags,
    project=excluded.project,
    context=excluded.context,
    parent_id=excluded.parent_id,
    sprint_id=excluded.sprint_id,
    created_at=excluded.created_at,
    updated_at=excluded.updated_at,
    iteration_id=excluded.iteration_id,
    file_path=excluded.file_path`, rowArgs(row)...)
	return err
}

// ReplaceAll truncates and reloads the tasks table atomically.
func (s *Store) ReplaceAll(ctx context.Context, rows []TaskRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, `DELETE FROM tasks`); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO tasks (
    id, title, status, priority, type, tags, project, context, parent_id, sprint_id, created_at, updated_at, iteration_id, file_path
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, row := range rows {
		if _, err = stmt.ExecContext(ctx, rowArgs(row)...); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ListTasks returns rows filtered per filters.
func (s *Store) ListTasks(ctx context.Context, f Filters) ([]TaskRow, error) {
	query := strings.Builder{}
	query.WriteString(`SELECT id, title, status, priority, type, tags, project, context, parent_id, sprint_id, created_at, updated_at, iteration_id, file_path FROM tasks`)

	var clauses []string
	var args []interface{}

	if len(f.Statuses) > 0 {
		clauses = append(clauses, inClause("status", len(f.Statuses)))
		for _, val := range f.Statuses {
			args = append(args, val)
		}
	}
	if len(f.Types) > 0 {
		clauses = append(clauses, inClause("type", len(f.Types)))
		for _, val := range f.Types {
			args = append(args, val)
		}
	}
	if len(f.Priority) > 0 {
		clauses = append(clauses, inClause("priority", len(f.Priority)))
		for _, val := range f.Priority {
			args = append(args, val)
		}
	}
	if len(f.SprintIDs) > 0 {
		clauses = append(clauses, inClause("sprint_id", len(f.SprintIDs)))
		for _, val := range f.SprintIDs {
			args = append(args, val)
		}
	}
	if len(f.Tags) > 0 {
		var tagClauses []string
		for range f.Tags {
			tagClauses = append(tagClauses, "tags LIKE '%' || ? || '%'")
		}
		clauses = append(clauses, "("+strings.Join(tagClauses, " AND ")+")")
		for _, val := range f.Tags {
			args = append(args, val)
		}
	}

	if len(clauses) > 0 {
		query.WriteString(" WHERE ")
		query.WriteString(strings.Join(clauses, " AND "))
	}
	query.WriteString(" ORDER BY updated_at DESC, id DESC")
	if f.Limit > 0 {
		query.WriteString(" LIMIT ?")
		args = append(args, f.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []TaskRow
	for rows.Next() {
		var row TaskRow
		if err := rows.Scan(
			&row.ID,
			&row.Title,
			&row.Status,
			&row.Priority,
			&row.Type,
			&row.Tags,
			&row.Project,
			&row.Context,
			&row.ParentID,
			&row.SprintID,
			&row.CreatedAt,
			&row.UpdatedAt,
			&row.IterationID,
			&row.FilePath,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}

	return result, rows.Err()
}

func rowArgs(row TaskRow) []interface{} {
	return []interface{}{
		row.ID,
		row.Title,
		row.Status,
		row.Priority,
		row.Type,
		row.Tags,
		row.Project,
		row.Context,
		row.ParentID,
		row.SprintID,
		row.CreatedAt,
		row.UpdatedAt,
		row.IterationID,
		row.FilePath,
	}
}

func inClause(column string, count int) string {
	var b strings.Builder
	b.WriteString(column)
	b.WriteString(" IN (")
	for i := 0; i < count; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("?")
	}
	b.WriteString(")")
	return b.String()
}
