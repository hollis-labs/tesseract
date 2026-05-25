package embedding

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
)

// PgVectorIndex implements VectorIndex using PostgreSQL with the pgvector extension.
type PgVectorIndex struct {
	db         *sql.DB
	dimensions int
}

// PgVectorConfig configures the pgvector index.
type PgVectorConfig struct {
	// ConnString is the PostgreSQL connection string (e.g. "postgres://user:pass@host:5432/tesseract").
	ConnString string
	// Dimensions is the expected vector dimensionality. Used for schema creation.
	Dimensions int
}

// NewPgVectorIndex creates a pgvector-backed vector index.
// The caller must supply an already-opened *sql.DB with the pgvector extension enabled.
func NewPgVectorIndex(db *sql.DB, dimensions int) (*PgVectorIndex, error) {
	idx := &PgVectorIndex{db: db, dimensions: dimensions}
	if err := idx.ensureSchema(context.Background()); err != nil {
		return nil, fmt.Errorf("pgvector: schema setup: %w", err)
	}
	return idx, nil
}

func (p *PgVectorIndex) ensureSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE EXTENSION IF NOT EXISTS vector`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS tesseract_vectors (
			record_id  TEXT NOT NULL,
			namespace  TEXT NOT NULL DEFAULT '',
			key_name   TEXT NOT NULL DEFAULT '',
			record_type TEXT NOT NULL DEFAULT '',
			model      TEXT NOT NULL,
			dimensions INTEGER NOT NULL,
			vector     vector(%d),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (record_id, model)
		)`, p.dimensions),
		`CREATE INDEX IF NOT EXISTS idx_tesseract_vectors_vec ON tesseract_vectors USING ivfflat (vector vector_cosine_ops) WITH (lists = 100)`,
		`CREATE INDEX IF NOT EXISTS idx_tesseract_vectors_ns ON tesseract_vectors (namespace)`,
		`CREATE INDEX IF NOT EXISTS idx_tesseract_vectors_type ON tesseract_vectors (record_type)`,
	}
	for _, stmt := range stmts {
		if _, err := p.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt[:min(len(stmt), 60)], err)
		}
	}
	return nil
}

func (p *PgVectorIndex) Upsert(ctx context.Context, entry VectorEntry) error {
	vecStr := pgvectorString(entry.Vector)
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO tesseract_vectors (record_id, namespace, key_name, record_type, model, dimensions, vector)
		VALUES ($1, $2, $3, $4, $5, $6, $7::vector)
		ON CONFLICT (record_id, model) DO UPDATE SET
			namespace = EXCLUDED.namespace,
			key_name = EXCLUDED.key_name,
			record_type = EXCLUDED.record_type,
			dimensions = EXCLUDED.dimensions,
			vector = EXCLUDED.vector,
			created_at = now()`,
		entry.RecordID, entry.Namespace, entry.Key, entry.RecordType,
		entry.Model, entry.Dimensions, vecStr)
	return err
}

func (p *PgVectorIndex) Search(ctx context.Context, query []float32, opts SearchOptions) ([]SearchResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}

	vecStr := pgvectorString(query)

	var conditions []string
	var args []any
	argIdx := 2 // $1 is the query vector

	args = append(args, vecStr)

	if opts.Model != "" {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("model = $%d", argIdx))
		args = append(args, opts.Model)
	}

	for _, ns := range opts.Namespaces {
		argIdx++
		if strings.HasSuffix(ns, "*") {
			conditions = append(conditions, fmt.Sprintf("namespace LIKE $%d", argIdx))
			args = append(args, strings.TrimSuffix(ns, "*")+"%")
		} else {
			conditions = append(conditions, fmt.Sprintf("namespace = $%d", argIdx))
			args = append(args, ns)
		}
	}

	for _, rt := range opts.Types {
		argIdx++
		conditions = append(conditions, fmt.Sprintf("record_type = $%d", argIdx))
		args = append(args, rt)
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// pgvector uses <=> for cosine distance (1 - similarity).
	query_ := fmt.Sprintf(` //nolint:gosec // where clause built from validated params, limit is an int
		SELECT record_id, namespace, key_name, 1 - (vector <=> $1::vector) AS similarity
		FROM tesseract_vectors
		%s
		ORDER BY vector <=> $1::vector
		LIMIT %d`, where, limit)

	rows, err := p.db.QueryContext(ctx, query_, args...)
	if err != nil {
		return nil, fmt.Errorf("pgvector search: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.RecordID, &r.Namespace, &r.Key, &r.Score); err != nil {
			return nil, fmt.Errorf("pgvector scan: %w", err)
		}
		if opts.Threshold > 0 && r.Score < opts.Threshold {
			continue
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (p *PgVectorIndex) Delete(ctx context.Context, recordID, model string) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM tesseract_vectors WHERE record_id = $1 AND model = $2`, recordID, model)
	return err
}

func (p *PgVectorIndex) Close() error {
	return p.db.Close()
}

// pgvectorString converts a float32 slice to pgvector's text format: "[0.1,0.2,0.3]".
func pgvectorString(vec []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		// Use binary conversion for exact float representation.
		bits := math.Float32bits(v)
		f := math.Float32frombits(bits)
		fmt.Fprintf(&b, "%g", f)
	}
	b.WriteByte(']')
	return b.String()
}
