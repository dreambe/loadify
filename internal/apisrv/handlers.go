package apisrv

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/sla"
	"github.com/dreambe/loadify/internal/store"
	"github.com/dreambe/loadify/internal/store/postgres"
	"github.com/go-chi/chi/v5"
)

// apiError carries an HTTP status with a message so shared helpers can return
// errors that handlers map to the right code.
type apiError struct {
	code int
	msg  string
}

func (e apiError) Error() string { return e.msg }

func errNotFound(m string) error   { return apiError{http.StatusNotFound, m} }
func errBadRequest(m string) error { return apiError{http.StatusBadRequest, m} }
func errUnavailable(m string) error { return apiError{http.StatusServiceUnavailable, m} }

func statusCodeFor(err error) int {
	var a apiError
	if errors.As(err, &a) {
		return a.code
	}
	return http.StatusInternalServerError
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// --- test definitions ---

type createTestReq struct {
	Name       string          `json:"name"`
	Protocol   string          `json:"protocol"`
	Plan       json.RawMessage `json:"plan"`
	Ramp       json.RawMessage `json:"ramp"`
	Script     string          `json:"script,omitempty"`
	Thresholds json.RawMessage `json:"thresholds,omitempty"`
	Dataset    json.RawMessage `json:"dataset,omitempty"`
}

func (s *Server) handleCreateTest(w http.ResponseWriter, r *http.Request) {
	var req createTestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	// Validate the plan up-front.
	if _, err := plan.Parse(req.Plan); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	id, err := s.pg.CreateTestDefinition(ctx, &postgres.TestDefinition{
		Name:       req.Name,
		Protocol:   req.Protocol,
		PlanJSON:   req.Plan,
		RampJSON:   req.Ramp,
		ScriptJS:   req.Script,
		Thresholds: req.Thresholds,
		DataJSON:   req.Dataset,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) handleListTests(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	tds, err := s.pg.ListTestDefinitions(ctx, 100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tds)
}

func (s *Server) handleGetTest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	td, err := s.pg.GetTestDefinition(ctx, chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, td)
}

// --- runs ---

type startRunReq struct {
	TestID         string `json:"test_id"`
	DesiredWorkers int    `json:"desired_workers"`
}

func (s *Server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	var req startRunReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()

	runID, runStatus, err := s.launchRun(ctx, req.TestID, req.DesiredWorkers)
	if err != nil {
		writeErr(w, statusCodeFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"run_id": runID, "status": runStatus})
}

// launchRun starts a run for a test definition and returns its id and status
// ("running"/"queued"). Shared by the REST handler and the scheduler.
func (s *Server) launchRun(ctx context.Context, testID string, workers int) (string, string, error) {
	td, err := s.pg.GetTestDefinition(ctx, testID)
	if err != nil {
		return "", "", errNotFound("test not found")
	}
	p, err := plan.Parse(td.PlanJSON)
	if err != nil {
		return "", "", errBadRequest(err.Error())
	}
	ramp, err := parseRamp(td.RampJSON)
	if err != nil {
		return "", "", errBadRequest(err.Error())
	}

	runID, err := s.pg.CreateRun(ctx, td.ID, workers)
	if err != nil {
		return "", "", err
	}

	var script *loadifyv1.ScriptBundle
	if td.ScriptJS != "" {
		script = &loadifyv1.ScriptBundle{MainJs: td.ScriptJS}
		if len(td.DataJSON) > 0 {
			script.Modules = map[string]string{"__data__": string(td.DataJSON)}
		}
	}
	resp, err := s.coord.StartRun(ctx, &loadifyv1.StartRunRequest{
		RunId:          runID,
		Protocol:       protoEnum(p.Protocol),
		PlanJson:       td.PlanJSON,
		Ramp:           ramp,
		Script:         script,
		DesiredWorkers: int32(workers),
	})
	if err != nil {
		_, _ = s.pg.FinishRun(context.Background(), runID, "failed", json.RawMessage(`{"error":"dispatch failed"}`))
		return "", "", errUnavailable(err.Error())
	}

	if resp != nil && resp.Status == "queued" {
		_ = s.pg.SetRunStatus(ctx, runID, "queued")
		return runID, "queued", nil
	}
	_ = s.pg.SetRunRunning(ctx, runID)
	go s.watchRun(runID)
	return runID, "running", nil
}

// watchRun blocks on the live stream; when it closes the run is finished, so we
// finalize it. If apisrv restarts and loses this goroutine, the reaper
// (StartReaper) finalizes the orphaned run instead.
func (s *Server) watchRun(runID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	stream, err := s.coord.StreamLive(ctx, &loadifyv1.LiveRequest{RunId: runID})
	if err == nil {
		for {
			if _, rerr := stream.Recv(); rerr != nil {
				break
			}
		}
	}
	// Allow rollups to flush, then summarize.
	time.Sleep(2 * time.Second)
	s.finalizeRun(runID, "completed")
}

// finalizeRun computes a run's summary, evaluates SLA thresholds and marks the
// run terminal. It is idempotent (FinishRun is a no-op once terminal), so the
// watcher and the reaper may both call it safely.
func (s *Server) finalizeRun(runID, status string) {
	sctx, scancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer scancel()
	summary, total, serr := s.ch.Summary(sctx, runID)
	payload := map[string]any{"total_requests": total, "summary": summary}
	if serr != nil {
		s.log.Warn("run summary failed", "run", runID, "err", serr)
	}
	if passed, checks, ok := s.evaluateThresholds(sctx, runID, summary, total); ok {
		payload["passed"] = passed
		payload["checks"] = checks
		if !passed {
			status = "failed"
		}
	}
	body, _ := json.Marshal(payload)
	switched, err := s.pg.FinishRun(sctx, runID, status, body)
	if err != nil {
		s.log.Warn("finish run failed", "run", runID, "err", err)
		return
	}
	if switched {
		s.log.Info("run finalized", "run", runID, "total", total, "status", status)
		s.notifyWebhook(runID, status, payload)
	}
}

// StartReaper periodically reconciles runs left active by an apisrv restart:
// a run the coordinator reports COMPLETED, no longer knows about, or that has
// outlived maxRunAge is finalized so it never stays "running" forever.
func (s *Server) StartReaper(ctx context.Context, interval, maxRunAge time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.reapOnce(ctx, maxRunAge)
		}
	}
}

func (s *Server) reapOnce(ctx context.Context, maxRunAge time.Duration) {
	lctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	runs, err := s.pg.ListActiveRuns(lctx)
	if err != nil {
		s.log.Warn("reaper: list active runs failed", "err", err)
		return
	}
	for _, r := range runs {
		// Give a freshly-started run time to register before reconciling.
		if time.Since(r.CreatedAt) < 15*time.Second {
			continue
		}
		st, serr := s.coord.GetRunState(lctx, &loadifyv1.RunStateRequest{RunId: r.ID})
		overdue := time.Since(r.CreatedAt) > maxRunAge
		switch {
		case serr != nil:
			// Coordinator doesn't know it (restarted / cleaned up). Finalize
			// from whatever metrics exist.
			s.finalizeRun(r.ID, "completed")
		case st.Status == loadifyv1.RunStatus_RUN_STATUS_COMPLETED:
			s.finalizeRun(r.ID, "completed")
		case st.Status == loadifyv1.RunStatus_RUN_STATUS_RUNNING:
			// A queued run the coordinator has now dispatched: reflect it.
			if r.Status != "running" {
				_ = s.pg.SetRunRunning(lctx, r.ID)
			}
		case st.Status == loadifyv1.RunStatus_RUN_STATUS_QUEUED:
			if overdue {
				s.log.Warn("reaper: aborting overdue queued run", "run", r.ID)
				s.finalizeRun(r.ID, "aborted")
			}
		case overdue:
			s.log.Warn("reaper: aborting overdue run", "run", r.ID, "age", time.Since(r.CreatedAt))
			s.finalizeRun(r.ID, "aborted")
		}
	}
}

// evaluateThresholds loads the run's test thresholds and checks them against the
// summary. ok is false when there are no thresholds (nothing to evaluate).
func (s *Server) evaluateThresholds(ctx context.Context, runID string, summary store.SeriesPoint, total int64) (bool, []sla.Check, bool) {
	run, err := s.pg.GetRun(ctx, runID)
	if err != nil {
		return false, nil, false
	}
	td, err := s.pg.GetTestDefinition(ctx, run.TestDefID)
	if err != nil || len(td.Thresholds) == 0 {
		return false, nil, false
	}
	var ths []sla.Threshold
	if err := json.Unmarshal(td.Thresholds, &ths); err != nil || len(ths) == 0 {
		return false, nil, false
	}
	qps := 0.0
	if run.StartedAt != nil {
		if elapsed := time.Since(*run.StartedAt).Seconds(); elapsed > 0 {
			qps = float64(total) / elapsed
		}
	}
	passed, checks := sla.Evaluate(ths, sla.Metrics{
		P50ms:     summary.P50ms,
		P90ms:     summary.P90ms,
		P95ms:     summary.P95ms,
		P99ms:     summary.P99ms,
		ErrorRate: summary.ErrorRate * 100, // fraction -> percent
		QPS:       qps,
	})
	return passed, checks, true
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	runs, err := s.pg.ListRuns(ctx, 100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	run, err := s.pg.GetRun(ctx, chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleRunSeries(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	group := r.URL.Query().Get("group")
	res, _ := strconv.Atoi(r.URL.Query().Get("res"))
	if res <= 0 {
		res = 1
	}
	to := time.Now()
	from := to.Add(-24 * time.Hour)
	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	pts, err := s.ch.QuerySeries(ctx, runID, group, from, to, res)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pts)
}

// handleRunExport streams a run's per-second series as CSV for offline
// analysis (spreadsheets, notebooks, attaching to reports).
func (s *Server) handleRunExport(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	group := r.URL.Query().Get("group")
	res, _ := strconv.Atoi(r.URL.Query().Get("res"))
	if res <= 0 {
		res = 1
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()

	run, err := s.pg.GetRun(ctx, runID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	from := run.CreatedAt.Add(-time.Minute)
	to := time.Now()
	if run.EndedAt != nil {
		to = run.EndedAt.Add(time.Minute)
	}
	pts, err := s.ch.QuerySeries(ctx, runID, group, from, to, res)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="run-`+runID+`.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"ts", "qps", "error_rate", "p50_ms", "p90_ms", "p95_ms", "p99_ms"})
	for _, p := range pts {
		_ = cw.Write([]string{
			p.TS.UTC().Format(time.RFC3339),
			strconv.FormatFloat(p.RPS, 'f', 2, 64),
			strconv.FormatFloat(p.ErrorRate, 'f', 4, 64),
			strconv.FormatFloat(p.P50ms, 'f', 2, 64),
			strconv.FormatFloat(p.P90ms, 'f', 2, 64),
			strconv.FormatFloat(p.P95ms, 'f', 2, 64),
			strconv.FormatFloat(p.P99ms, 'f', 2, 64),
		})
	}
	cw.Flush()
}

func (s *Server) handleListWorkers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	resp, err := s.coord.ListWorkers(ctx, &loadifyv1.ListWorkersRequest{})
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp.Workers)
}

// --- helpers ---

type rampStageJSON struct {
	DurationMs int64 `json:"duration_ms"`
	TargetVUs  int64 `json:"target_vus"`
	TargetRPS  int64 `json:"target_rps"`
}

func parseRamp(data []byte) ([]*loadifyv1.RampStage, error) {
	if len(data) == 0 {
		// Default: 10 VUs for 30s.
		return []*loadifyv1.RampStage{{DurationMs: 30000, TargetVus: 10}}, nil
	}
	var stages []rampStageJSON
	if err := json.Unmarshal(data, &stages); err != nil {
		return nil, err
	}
	out := make([]*loadifyv1.RampStage, 0, len(stages))
	for _, st := range stages {
		out = append(out, &loadifyv1.RampStage{DurationMs: st.DurationMs, TargetVus: st.TargetVUs, TargetRps: st.TargetRPS})
	}
	return out, nil
}

func protoEnum(p plan.Protocol) loadifyv1.Protocol {
	switch p {
	case plan.HTTP:
		return loadifyv1.Protocol_PROTOCOL_HTTP
	case plan.HTTPS:
		return loadifyv1.Protocol_PROTOCOL_HTTPS
	case plan.GRPC:
		return loadifyv1.Protocol_PROTOCOL_GRPC
	case plan.WebSocket:
		return loadifyv1.Protocol_PROTOCOL_WEBSOCKET
	case plan.SSE:
		return loadifyv1.Protocol_PROTOCOL_SSE
	case plan.Script:
		// Script runs are protocol-agnostic; UNSPECIFIED lets the scheduler use
		// any healthy worker and the worker selects the script driver.
		return loadifyv1.Protocol_PROTOCOL_UNSPECIFIED
	default:
		return loadifyv1.Protocol_PROTOCOL_UNSPECIFIED
	}
}
