// Package clickhouse implements the metrics store (rollup writes + series reads).
package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/dreambe/loadify/internal/config"
	"github.com/dreambe/loadify/internal/store"
	"github.com/dreambe/loadify/migrations"
)

// Store is a ClickHouse-backed metrics store.
type Store struct {
	conn driver.Conn
}

// Connect opens a ClickHouse connection.
func Connect(ctx context.Context, cfg config.ClickHouse) (*Store, error) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{cfg.Addr},
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.Username,
			Password: cfg.Password,
		},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("clickhouse: open: %w", err)
	}
	if err := conn.Ping(ctx); err != nil {
		return nil, fmt.Errorf("clickhouse: ping: %w", err)
	}
	return &Store{conn: conn}, nil
}

// Close releases the connection.
func (s *Store) Close() error { return s.conn.Close() }

// Migrate applies the embedded ClickHouse DDL (idempotent).
func (s *Store) Migrate(ctx context.Context) error {
	stmts, err := migrations.Statements("clickhouse")
	if err != nil {
		return err
	}
	for _, stmt := range stmts {
		if err := s.conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("clickhouse: migrate: %w", err)
		}
	}
	return nil
}

// WriteRollups batch-inserts finalized per-second rollups.
func (s *Store) WriteRollups(ctx context.Context, rows []store.Rollup) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO rollup_1s "+
		"(run_id, ts, `group`, protocol, status_class, count, errors, sent_bytes, recv_bytes, "+
		"p50_ms, p90_ms, p95_ms, p99_ms, max_ms, hist)")
	if err != nil {
		return fmt.Errorf("clickhouse: prepare: %w", err)
	}
	for _, r := range rows {
		if err := batch.Append(
			r.RunID, r.TS, r.Group, r.Protocol, r.StatusClass,
			uint64(r.Count), uint64(r.Errors), uint64(r.SentBytes), uint64(r.RecvBytes),
			r.P50ms, r.P90ms, r.P95ms, r.P99ms, r.MaxMs, string(r.Hist),
		); err != nil {
			return fmt.Errorf("clickhouse: append: %w", err)
		}
	}
	return batch.Send()
}

// WriteSamples batch-inserts the bounded, sampled request detail used for
// post-run error drill-down. Rows expire via the table TTL.
func (s *Store) WriteSamples(ctx context.Context, rows []store.Sample) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO samples "+
		"(run_id, ts, `group`, protocol, status_class, status, ok, error_kind, method, url, latency_us, recv_bytes, resp_body, req_body)")
	if err != nil {
		return fmt.Errorf("clickhouse: prepare samples: %w", err)
	}
	for _, r := range rows {
		var ok uint8
		if r.OK {
			ok = 1
		}
		if err := batch.Append(
			r.RunID, r.TS, r.Group, r.Protocol, r.StatusClass, r.Status, ok,
			r.ErrorKind, r.Method, r.URL, r.LatencyUs, r.RecvBytes, r.RespBody, r.ReqBody,
		); err != nil {
			return fmt.Errorf("clickhouse: append sample: %w", err)
		}
	}
	return batch.Send()
}

// QuerySamples returns sampled request detail for a run, optionally filtered by
// group / status_class / error_kind, newest first.
func (s *Store) QuerySamples(ctx context.Context, runID string, f store.SampleFilter) ([]store.Sample, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := "SELECT ts, `group`, protocol, status_class, status, ok, error_kind, method, url, latency_us, recv_bytes, resp_body, req_body " +
		"FROM samples WHERE run_id = ?"
	args := []any{runID}
	if f.Group != "" && f.Group != "*" {
		q += " AND `group` = ?"
		args = append(args, f.Group)
	}
	if f.StatusClass != "" {
		q += " AND status_class = ?"
		args = append(args, f.StatusClass)
	}
	if f.ErrorKind != "" {
		q += " AND error_kind = ?"
		args = append(args, f.ErrorKind)
	}
	q += " ORDER BY ts DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: query samples: %w", err)
	}
	defer rows.Close()
	out := []store.Sample{}
	for rows.Next() {
		var r store.Sample
		var ok uint8
		if err := rows.Scan(&r.TS, &r.Group, &r.Protocol, &r.StatusClass, &r.Status, &ok,
			&r.ErrorKind, &r.Method, &r.URL, &r.LatencyUs, &r.RecvBytes, &r.RespBody, &r.ReqBody); err != nil {
			return nil, err
		}
		r.OK = ok == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// QuerySeries returns a time-bucketed series for a run. group "" or "*" means
// all groups combined. resSeconds is the bucket width.
func (s *Store) QuerySeries(ctx context.Context, runID, group string, from, to time.Time, resSeconds int) ([]store.SeriesPoint, error) {
	if resSeconds <= 0 {
		resSeconds = 1
	}
	where := "run_id = ? AND ts >= ? AND ts <= ?"
	args := []any{runID, from, to}
	if group != "" && group != "*" {
		where += " AND `group` = ?"
		args = append(args, group)
	}
	// Count-weighted percentile averaging keeps the query cheap; per-second
	// single-group rows are exact, coarser buckets are approximate.
	query := fmt.Sprintf(`
		SELECT
			toStartOfInterval(ts, INTERVAL %d second) AS bucket,
			sum(count) AS cnt,
			sum(errors) AS errs,
			sum(p50_ms * count) / sum(count) AS p50,
			sum(p90_ms * count) / sum(count) AS p90,
			sum(p95_ms * count) / sum(count) AS p95,
			sum(p99_ms * count) / sum(count) AS p99
		FROM rollup_1s
		WHERE %s
		GROUP BY bucket
		ORDER BY bucket`, resSeconds, where)

	rrows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: query: %w", err)
	}
	defer rrows.Close()

	var out []store.SeriesPoint
	for rrows.Next() {
		var (
			bucket    time.Time
			cnt, errs uint64
			p50, p90, p95, p99 float64
		)
		if err := rrows.Scan(&bucket, &cnt, &errs, &p50, &p90, &p95, &p99); err != nil {
			return nil, err
		}
		errRate := 0.0
		if cnt > 0 {
			errRate = float64(errs) / float64(cnt)
		}
		out = append(out, store.SeriesPoint{
			TS:        bucket,
			RPS:       float64(cnt) / float64(resSeconds),
			ErrorRate: errRate,
			P50ms:     p50,
			P90ms:     p90,
			P95ms:     p95,
			P99ms:     p99,
		})
	}
	return out, rrows.Err()
}

// Summary returns aggregate totals for a finished run.
func (s *Store) Summary(ctx context.Context, runID string) (store.SeriesPoint, int64, error) {
	row := s.conn.QueryRow(ctx, `
		SELECT
			sum(count) AS cnt,
			sum(errors) AS errs,
			sum(p50_ms * count) / sum(count) AS p50,
			sum(p90_ms * count) / sum(count) AS p90,
			sum(p95_ms * count) / sum(count) AS p95,
			sum(p99_ms * count) / sum(count) AS p99
		FROM rollup_1s WHERE run_id = ?`, runID)
	var (
		cnt, errs uint64
		p50, p90, p95, p99 float64
	)
	if err := row.Scan(&cnt, &errs, &p50, &p90, &p95, &p99); err != nil {
		return store.SeriesPoint{}, 0, err
	}
	errRate := 0.0
	if cnt > 0 {
		errRate = float64(errs) / float64(cnt)
	}
	return store.SeriesPoint{ErrorRate: errRate, P50ms: p50, P90ms: p90, P95ms: p95, P99ms: p99}, int64(cnt), nil
}
