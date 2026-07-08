// Package clickhouse implements the metrics store (rollup writes + series reads).
package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/dreambe/loadify/internal/config"
	"github.com/dreambe/loadify/internal/metrics"
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
	if !f.From.IsZero() {
		q += " AND ts >= ?"
		args = append(args, f.From)
	}
	if !f.To.IsZero() {
		q += " AND ts <= ?"
		args = append(args, f.To)
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
	} else {
		// Combined view: exclude the transaction pseudo-group (sum-of-steps).
		where += " AND " + notTxn
	}
	// Percentiles are not averageable, and a single bucket aggregates several
	// (group, status_class) rows even at 1s resolution — so merge the stored
	// per-second histograms per bucket and read exact percentiles off the merge,
	// rather than count-weighting the per-row p*_ms columns.
	query := fmt.Sprintf(`
		SELECT toStartOfInterval(ts, INTERVAL %d second) AS bucket, count, errors, hist
		FROM rollup_1s
		WHERE %s
		ORDER BY bucket`, resSeconds, where)

	rrows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: query: %w", err)
	}
	defer rrows.Close()

	var out []store.SeriesPoint
	var curBucket time.Time
	var have bool
	var cnt, errs uint64
	merged := metrics.NewHistogram()
	flush := func() {
		if !have {
			return
		}
		pct := metrics.PercentilesOf(merged)
		errRate := 0.0
		if cnt > 0 {
			errRate = float64(errs) / float64(cnt)
		}
		out = append(out, store.SeriesPoint{
			TS:        curBucket,
			RPS:       float64(cnt) / float64(resSeconds),
			ErrorRate: errRate,
			P50ms:     pct.P50,
			P90ms:     pct.P90,
			P95ms:     pct.P95,
			P99ms:     pct.P99,
		})
	}
	for rrows.Next() {
		var (
			bucket time.Time
			c, e   uint64
			blob   []byte
		)
		if err := rrows.Scan(&bucket, &c, &e, &blob); err != nil {
			return nil, err
		}
		if !have || !bucket.Equal(curBucket) {
			flush()
			curBucket, have, cnt, errs = bucket, true, 0, 0
			merged = metrics.NewHistogram()
		}
		cnt += c
		errs += e
		if h := metrics.DecodeHistogram(blob); h != nil {
			merged.Merge(h)
		}
	}
	flush()
	return out, rrows.Err()
}

// mergedPercentiles merges the stored per-second HdrHistograms matching the
// WHERE clause and computes exact latency percentiles from the combined
// distribution. Percentiles are NOT averageable, so this is the only correct
// way to get a run's (or a coarse bucket's) tail — the per-second p*_ms columns
// are exact only within their own 1-second window.
func (s *Store) mergedPercentiles(ctx context.Context, where string, args []any) (metrics.Percentiles, error) {
	merged := metrics.NewHistogram()
	rows, err := s.conn.Query(ctx, "SELECT hist FROM rollup_1s WHERE "+where+" AND length(hist) > 0", args...)
	if err != nil {
		return metrics.Percentiles{}, fmt.Errorf("clickhouse: read hists: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return metrics.Percentiles{}, err
		}
		if h := metrics.DecodeHistogram(blob); h != nil {
			merged.Merge(h)
		}
	}
	return metrics.PercentilesOf(merged), rows.Err()
}

// notTxn excludes the scenario transaction pseudo-group (its latency is the sum
// of the steps, so it must never pollute the request-level headline).
const notTxn = "`group` NOT LIKE 'txn:%'"

// Summary returns aggregate totals for a finished run: exact percentiles from
// the merged histogram, request-level counts (transaction rows excluded).
func (s *Store) Summary(ctx context.Context, runID string) (store.SeriesPoint, int64, error) {
	var cnt, errs uint64
	if err := s.conn.QueryRow(ctx,
		"SELECT sum(count), sum(errors) FROM rollup_1s WHERE run_id = ? AND "+notTxn, runID,
	).Scan(&cnt, &errs); err != nil {
		return store.SeriesPoint{}, 0, err
	}
	pct, err := s.mergedPercentiles(ctx, "run_id = ? AND "+notTxn, []any{runID})
	if err != nil {
		return store.SeriesPoint{}, 0, err
	}
	errRate := 0.0
	if cnt > 0 {
		errRate = float64(errs) / float64(cnt)
	}
	return store.SeriesPoint{ErrorRate: errRate, P50ms: pct.P50, P90ms: pct.P90, P95ms: pct.P95, P99ms: pct.P99}, int64(cnt), nil
}

// DeleteRun purges a run's rollups and samples. Uses lightweight DELETE
// (ClickHouse 24.x); metrics are TTL-bounded anyway, so the caller treats a
// failure here as non-fatal.
func (s *Store) DeleteRun(ctx context.Context, runID string) error {
	for _, tbl := range []string{"rollup_1s", "samples"} {
		if err := s.conn.Exec(ctx, "DELETE FROM "+tbl+" WHERE run_id = ?", runID); err != nil {
			return fmt.Errorf("clickhouse: delete %s for run %s: %w", tbl, runID, err)
		}
	}
	return nil
}
