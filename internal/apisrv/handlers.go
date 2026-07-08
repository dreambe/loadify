package apisrv

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/auth"
	"github.com/dreambe/loadify/internal/importer"
	"github.com/dreambe/loadify/internal/plan"
	scriptpkg "github.com/dreambe/loadify/internal/script"
	"github.com/dreambe/loadify/internal/sla"
	"github.com/dreambe/loadify/internal/store"
	"github.com/dreambe/loadify/internal/store/postgres"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"
)

// apiError carries an HTTP status with a message so shared helpers can return
// errors that handlers map to the right code.
type apiError struct {
	code int
	msg  string
}

func (e apiError) Error() string { return e.msg }

func errNotFound(m string) error    { return apiError{http.StatusNotFound, m} }
func errBadRequest(m string) error  { return apiError{http.StatusBadRequest, m} }
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
	Tags       []string        `json:"tags,omitempty"`
}

// validateDataset rejects a data feeder that isn't an array of flat JSON
// objects, so the mistake surfaces at save time instead of as a worker-side
// run failure.
func validateDataset(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return errors.New(`dataset must be a JSON array of objects, e.g. [{"user":"alice"}]`)
	}
	return nil
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
	if err := validateDataset(req.Dataset); err != nil {
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
		Tags:       normalizeTags(req.Tags),
		CreatedBy:  callerID(r),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// handleUpdateTest rewrites an existing test definition in place.
// canMutate implements the "shared read, owner-or-admin write" policy: any
// authenticated user may view every resource, but only its creator or an admin
// may modify it. A nil owner (legacy rows predating ownership tracking) is
// admin-only, to fail safe.
func canMutate(c *auth.Claims, ownerID *string) bool {
	if c == nil {
		return false
	}
	if c.Role == auth.RoleAdmin {
		return true
	}
	return ownerID != nil && *ownerID == c.Subject
}

// denyIfNotOwner writes 403 and returns true when the caller may not mutate a
// resource owned by ownerID.
func (s *Server) denyIfNotOwner(w http.ResponseWriter, r *http.Request, ownerID *string) bool {
	c, _ := auth.FromContext(r.Context())
	if !canMutate(c, ownerID) {
		writeErr(w, http.StatusForbidden, "only the creator or an admin may modify this resource")
		return true
	}
	return false
}

func (s *Server) handleUpdateTest(w http.ResponseWriter, r *http.Request) {
	var req createTestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if _, err := plan.Parse(req.Plan); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateDataset(req.Dataset); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	existing, err := s.pg.GetTestDefinition(ctx, chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if s.denyIfNotOwner(w, r, existing.CreatedBy) {
		return
	}
	err = s.pg.UpdateTestDefinition(ctx, &postgres.TestDefinition{
		ID:         chi.URLParam(r, "id"),
		Name:       req.Name,
		Protocol:   req.Protocol,
		PlanJSON:   req.Plan,
		RampJSON:   req.Ramp,
		ScriptJS:   req.Script,
		Thresholds: req.Thresholds,
		DataJSON:   req.Dataset,
		Tags:       normalizeTags(req.Tags),
	})
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// normalizeTags trims, de-dupes and drops empty tags, capping the count so the
// label set stays a lightweight grouping aid rather than free-form metadata.
func normalizeTags(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) >= 12 {
			break
		}
	}
	return out
}

// handleDeleteTest archives a test definition: it disappears from lists and
// can no longer be run, but historical runs keep their reference.
func (s *Server) handleDeleteTest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	existing, err := s.pg.GetTestDefinition(ctx, chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if s.denyIfNotOwner(w, r, existing.CreatedBy) {
		return
	}
	if err := s.pg.ArchiveTestDefinition(ctx, chi.URLParam(r, "id")); err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleImport converts an external request format (curl/HAR/Postman/OpenAPI)
// into a test draft the builder prefills. It does NOT persist — the user
// reviews and saves via the normal create flow.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Format  string `json:"format"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	draft, err := importer.Parse(req.Format, req.Content)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

func (s *Server) handleListTests(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	tds, err := s.pg.ListTestDefinitions(ctx, 100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tds == nil {
		tds = []postgres.TestDefinition{}
	}
	writeJSON(w, http.StatusOK, tds)
}

// runMetrics is the compact per-run metric snapshot used for trend/baseline.
type runMetrics struct {
	Total     float64 `json:"total"`
	ErrorRate float64 `json:"error_rate"` // percent
	P50       float64 `json:"p50_ms"`
	P90       float64 `json:"p90_ms"`
	P95       float64 `json:"p95_ms"`
	P99       float64 `json:"p99_ms"`
}

// metricsFromSummary pulls the compact metrics out of a run's summary JSON.
func metricsFromSummary(summary json.RawMessage) runMetrics {
	var s struct {
		Total   float64 `json:"total_requests"`
		Summary struct {
			ErrorRate float64 `json:"error_rate"`
			P50ms     float64 `json:"p50_ms"`
			P90ms     float64 `json:"p90_ms"`
			P95ms     float64 `json:"p95_ms"`
			P99ms     float64 `json:"p99_ms"`
		} `json:"summary"`
	}
	_ = json.Unmarshal(summary, &s)
	return runMetrics{
		Total: s.Total, ErrorRate: s.Summary.ErrorRate * 100,
		P50: s.Summary.P50ms, P90: s.Summary.P90ms, P95: s.Summary.P95ms, P99: s.Summary.P99ms,
	}
}

type trendPoint struct {
	RunID   string     `json:"run_id"`
	Name    string     `json:"name"`
	Status  string     `json:"status"`
	EndedAt *time.Time `json:"ended_at,omitempty"`
	Metrics runMetrics `json:"metrics"`
}

// handleTestTrend returns a test's recent runs as compact metric points
// (oldest→newest) for trend charts.
func (s *Server) handleTestTrend(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 {
		n = 20
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	runs, err := s.pg.ListRunsByTest(ctx, chi.URLParam(r, "id"), n)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]trendPoint, 0, len(runs))
	// Reverse to oldest→newest so charts read left-to-right in time order.
	for i := len(runs) - 1; i >= 0; i-- {
		rn := runs[i]
		if rn.Status != "completed" && rn.Status != "failed" {
			continue
		}
		out = append(out, trendPoint{RunID: rn.ID, Name: rn.Name, Status: rn.Status, EndedAt: rn.EndedAt, Metrics: metricsFromSummary(rn.Summary)})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSetBaseline marks (run_id set) or clears (empty) a test's baseline run.
func (s *Server) handleSetBaseline(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	if err := s.pg.SetBaseline(ctx, chi.URLParam(r, "id"), req.RunID); err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

// callerID returns the authenticated user's id, or nil for system contexts.
func callerID(r *http.Request) *string {
	if c, ok := auth.FromContext(r.Context()); ok && c.Subject != "" {
		id := c.Subject
		return &id
	}
	return nil
}

type startRunReq struct {
	TestID         string `json:"test_id"`
	Name           string `json:"name"`
	DesiredWorkers int    `json:"desired_workers"`
	EnvironmentID  string `json:"environment_id"`
}

func (s *Server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	var req startRunReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()

	runID, runStatus, err := s.launchRun(ctx, req.TestID, req.DesiredWorkers, req.Name, callerID(r), "manual", req.EnvironmentID)
	if err != nil {
		writeErr(w, statusCodeFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"run_id": runID, "status": runStatus})
}

// launchRun starts a run for a test definition and returns its id and status
// ("running"/"queued"). Shared by the REST handler and the scheduler. An empty
// name falls back to "<test name> @ <time>"; createdBy is nil for the scheduler.
func (s *Server) launchRun(ctx context.Context, testID string, workers int, name string, createdBy *string, source, envID string) (string, string, error) {
	td, err := s.pg.GetTestDefinition(ctx, testID)
	if err != nil {
		return "", "", errNotFound("test not found")
	}
	if td.Archived {
		return "", "", errBadRequest("test has been deleted")
	}

	// Resolve a user-defined environment and substitute {{KEY}} placeholders
	// across the raw plan JSON and script before anything is parsed/dispatched,
	// so every protocol — not just scenarios — gets environment variables.
	planJSON := td.PlanJSON
	scriptJS := td.ScriptJS
	var envName string
	var envVars map[string]string
	if envID != "" {
		env, eerr := s.pg.GetEnvironment(ctx, envID)
		if eerr != nil {
			return "", "", errBadRequest("environment not found")
		}
		envName = env.Name
		envVars = env.Vars
		planJSON = json.RawMessage(substituteEnv(string(td.PlanJSON), env.Vars))
		scriptJS = substituteEnv(td.ScriptJS, env.Vars)
	}

	p, err := plan.Parse(planJSON)
	if err != nil {
		return "", "", errBadRequest(err.Error())
	}
	ramp, err := parseRamp(td.RampJSON)
	if err != nil {
		return "", "", errBadRequest(err.Error())
	}

	// Global once-setup: run the scenario's once_global steps a single time at
	// launch and fold the values they extract into the substitution map, so
	// {{var}} references resolve to literals for every worker (no per-iteration
	// login). Done before the run is created so a setup failure aborts cleanly
	// without a dangling run. The snapshot keeps the pre-setup plan (templates
	// intact) so a transient setup token is not persisted.
	snapPlanJSON := planJSON
	if p.Protocol == plan.Scenario {
		if gsteps := scriptpkg.GlobalSetupSteps(p.Scenario); len(gsteps) > 0 {
			vars, serr := scriptpkg.RunGlobalSetup(ctx, gsteps)
			if serr != nil {
				return "", "", errBadRequest("global setup failed: " + serr.Error())
			}
			if len(vars) > 0 {
				planJSON = json.RawMessage(substituteEnv(string(planJSON), vars))
				if p, err = plan.Parse(planJSON); err != nil {
					return "", "", errBadRequest(err.Error())
				}
			}
		}
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = td.Name + " @ " + time.Now().Format("01-02 15:04")
	}
	// Snapshot what actually runs — the env-substituted plan/script plus the
	// environment used — so the run stays reproducible even after the test or
	// environment is later edited (the original template alone wouldn't reveal
	// which target this run hit).
	snapshot := buildRunSnapshot(td, snapPlanJSON, scriptJS, envName, envVars)
	runID, err := s.pg.CreateRun(ctx, td.ID, workers, name, createdBy, source, snapshot)
	if err != nil {
		return "", "", err
	}

	var script *loadifyv1.ScriptBundle
	mainJS := scriptJS
	if p.Protocol == plan.Scenario {
		// Scenarios compile to a script and run on the script driver.
		js, cerr := scriptpkg.CompileScenario(p.Scenario)
		if cerr != nil {
			return "", "", errBadRequest(cerr.Error())
		}
		mainJS = js
	}
	if mainJS != "" {
		script = &loadifyv1.ScriptBundle{MainJs: mainJS}
	}
	// The dataset rides in the bundle's reserved "__data__" module for every
	// protocol: the script driver reads it itself; for plain protocols the
	// worker agent lifts it into the parsed plan so drivers (httpd) can feed
	// {{var}} tokens per request.
	if len(td.DataJSON) > 0 {
		if script == nil {
			script = &loadifyv1.ScriptBundle{}
		}
		script.Modules = map[string]string{"__data__": string(td.DataJSON)}
	}
	startReq := &loadifyv1.StartRunRequest{
		RunId:          runID,
		Protocol:       protoEnum(p.Protocol),
		PlanJson:       planJSON,
		Ramp:           ramp,
		Script:         script,
		DesiredWorkers: int32(workers),
	}
	if envName != "" {
		startReq.Env = map[string]string{"__environment__": envName}
	}
	// Persist the exact dispatch payload before dispatching, so a coordinator
	// restart that loses its in-memory queue can be replayed from Postgres (the
	// reaper does this). Cleared automatically when the run finalizes.
	if payload, merr := proto.Marshal(startReq); merr == nil {
		_ = s.pg.SetRunDispatch(ctx, runID, payload)
	}
	resp, err := s.coord.StartRun(ctx, startReq)
	if err != nil {
		_, _ = s.pg.FinishRun(context.Background(), runID, "failed", json.RawMessage(`{"error":"dispatch failed"}`))
		return "", "", errUnavailable(err.Error())
	}

	if resp != nil && resp.Status == "queued" {
		_ = s.pg.SetRunStatus(ctx, runID, "queued")
		return runID, "queued", nil
	}
	_ = s.pg.SetRunRunning(ctx, runID)
	go s.watchRun(runID, p.AlertOrDefault())
	return runID, "running", nil
}

// buildRunSnapshot records what a run actually executed: the test definition,
// but with the env-substituted plan/script (the resolved targets, not the
// {{KEY}} template) and a snapshot of the environment used. This keeps a run
// self-contained and reproducible regardless of later edits. The frontend reads
// snapshot.plan / snapshot.ramp / snapshot.environment.
func buildRunSnapshot(td *postgres.TestDefinition, planJSON json.RawMessage, scriptJS, envName string, envVars map[string]string) json.RawMessage {
	snap := map[string]any{}
	if b, err := json.Marshal(td); err == nil {
		_ = json.Unmarshal(b, &snap)
	}
	// Keep the UNRESOLVED plan template (with {{KEY}} placeholders) that
	// json.Marshal(td) already put in snap["plan"]. The interpolated planJSON
	// would embed substituted secret values, and the snapshot is served to any
	// viewer and through public share links — so it must never be persisted.
	_ = planJSON
	if scriptJS != "" {
		snap["script"] = scriptJS
	} else {
		delete(snap, "script")
	}
	if envName != "" {
		// Never persist environment values (secrets); keep the keys so the run
		// stays reproducible ("which vars, which env") without exposing them.
		maskedVars := make(map[string]string, len(envVars))
		for k := range envVars {
			maskedVars[k] = "••••••"
		}
		snap["environment"] = map[string]any{"name": envName, "vars": maskedVars}
	}
	// Datasets can be large and carry user PII; the snapshot keeps only the
	// row count (enough to know the run was data-driven), never the rows.
	if len(td.DataJSON) > 0 {
		delete(snap, "dataset")
		var rows []map[string]any
		if err := json.Unmarshal(td.DataJSON, &rows); err == nil {
			snap["dataset_rows"] = len(rows)
		}
	}
	out, err := json.Marshal(snap)
	if err != nil {
		// Fall back to the raw definition rather than losing the snapshot entirely.
		b, _ := json.Marshal(td)
		return b
	}
	return out
}

// watchRun follows a run's live stream and finalizes it when it TRULY ends.
// A broken stream does not by itself mean the run finished — the coordinator
// may have restarted while the worker keeps executing (and gets rehydrated).
// So on any stream break we consult the authoritative run state and only
// finalize on a terminal status; if it's still running we re-attach. This is
// what stops a transient coordinator disconnect / redeploy from finalizing a
// live run early as "completed". The reaper is the backstop if apisrv itself
// restarts and loses this goroutine.
func (s *Server) watchRun(runID string, alert plan.AlertConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	ev := newAlertEvaluator(alert)
	unresolved := 0 // consecutive times the coordinator couldn't be reached
	for {
		if stream, err := s.coord.StreamLive(ctx, &loadifyv1.LiveRequest{RunId: runID}); err == nil {
			for {
				tick, rerr := stream.Recv()
				if rerr != nil {
					break
				}
				// Early-warning alert: fire once when the error rate spikes mid-run.
				if rate, fire := ev.observe(tick); fire {
					s.log.Warn("run error-rate alert", "run", runID, "error_rate", rate)
					go s.notifyAlert(runID, rate)
				}
			}
		}
		// The stream ended. Decide from the authoritative state whether the run
		// is actually terminal or just briefly disconnected.
		st, serr := s.coord.GetRunState(context.Background(), &loadifyv1.RunStateRequest{RunId: runID})
		if serr != nil {
			// Coordinator unreachable (likely restarting). Retry a bounded while
			// before giving up to the reaper — never finalize on a blind guess.
			if unresolved++; unresolved > 20 {
				s.log.Warn("watchRun: run state unresolvable, leaving to reaper", "run", runID)
				return
			}
			time.Sleep(time.Second)
			continue
		}
		unresolved = 0
		switch st.Status {
		case loadifyv1.RunStatus_RUN_STATUS_PENDING, loadifyv1.RunStatus_RUN_STATUS_QUEUED, loadifyv1.RunStatus_RUN_STATUS_RUNNING:
			// Still live (e.g. rehydrated after a coordinator restart) — re-attach.
			time.Sleep(time.Second)
			continue
		case loadifyv1.RunStatus_RUN_STATUS_ABORTED:
			// Allow rollups to flush before summarizing.
			time.Sleep(2 * time.Second)
			s.finalizeRunReason(runID, "aborted", st.Reason)
			return
		default: // COMPLETED / FAILED / terminal
			time.Sleep(2 * time.Second)
			s.finalizeRunReason(runID, "completed", "")
			return
		}
	}
}

// regressP95Pct is the p95 increase over baseline that flags a regression.
const regressP95Pct = 20.0

// generatorHotCPUPct is the peak per-node CPU utilization (0-100 of total
// capacity) above which a run is flagged as possibly generator-limited: the
// measured latency may be the load generator's, not the target's.
const generatorHotCPUPct = 90.0

// attachBaseline, when the run's test has a baseline run, computes deltas vs the
// baseline and writes baseline/regressed into the summary payload. Best-effort.
func (s *Server) attachBaseline(ctx context.Context, runID string, total int64, summary store.SeriesPoint, payload map[string]any) {
	run, err := s.pg.GetRun(ctx, runID)
	if err != nil {
		return
	}
	td, err := s.pg.GetTestDefinition(ctx, run.TestDefID)
	if err != nil || td.BaselineRunID == nil || *td.BaselineRunID == "" || *td.BaselineRunID == runID {
		return
	}
	base, err := s.pg.GetRun(ctx, *td.BaselineRunID)
	if err != nil {
		return
	}
	bm := metricsFromSummary(base.Summary)
	pctChange := func(cur, baseV float64) float64 {
		if baseV == 0 {
			return 0
		}
		return (cur - baseV) / baseV * 100
	}
	curP95 := summary.P95ms
	p95Delta := pctChange(curP95, bm.P95)
	regressed := bm.P95 > 0 && p95Delta > regressP95Pct
	payload["baseline"] = map[string]any{
		"run_id":         *td.BaselineRunID,
		"p95_ms":         bm.P95,
		"p95_delta_pct":  p95Delta,
		"error_rate":     bm.ErrorRate,
		"total_requests": bm.Total,
	}
	payload["regressed"] = regressed
	if regressed {
		s.log.Info("run regressed vs baseline", "run", runID, "p95_delta_pct", p95Delta)
	}
}

// finalizeRun computes a run's summary, evaluates SLA thresholds and marks the
// run terminal. It is idempotent (FinishRun is a no-op once terminal), so the
// watcher and the reaper may both call it safely.
func (s *Server) finalizeRun(runID, status string) { s.finalizeRunReason(runID, status, "") }

// finalizeRunReason is finalizeRun with an optional abort reason recorded in
// the summary (used by the auto-stop circuit breaker).
func (s *Server) finalizeRunReason(runID, status, reason string) {
	sctx, scancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer scancel()
	summary, total, serr := s.ch.Summary(sctx, runID)
	payload := map[string]any{"total_requests": total, "summary": summary}
	if serr != nil {
		// A metrics-store failure must never be silent: the run still finalizes
		// (so it doesn't hang "running" forever), but it is flagged so the UI can
		// warn that the numbers are incomplete and not a basis for conclusions.
		s.log.Warn("run summary failed", "run", runID, "err", serr)
		payload["metrics_degraded"] = true
		payload["metrics_error"] = serr.Error()
	}
	if reason != "" {
		payload["auto_stopped"] = true
		payload["reason"] = reason
	}
	// Generator honesty: if the coordinator reports the load generator dropped
	// work or ran hot, flag it so a "green" result that may reflect the
	// generator's limits (not the target's) is not read as conclusive.
	if st, gerr := s.coord.GetRunState(sctx, &loadifyv1.RunStateRequest{RunId: runID}); gerr == nil {
		if st.DroppedIterations > 0 || st.DroppedMetrics > 0 || st.PeakCpuPct >= generatorHotCPUPct {
			payload["generator_saturated"] = true
			payload["dropped_iterations"] = st.DroppedIterations
			payload["dropped_metrics"] = st.DroppedMetrics
			payload["peak_cpu_pct"] = st.PeakCpuPct
		}
	}
	if passed, checks, ok := s.evaluateThresholds(sctx, runID, summary, total); ok {
		payload["passed"] = passed
		payload["checks"] = checks
		if !passed {
			status = "failed"
		}
	}
	// Compare against the test's baseline run, if one is set.
	s.attachBaseline(sctx, runID, total, summary, payload)
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
			// The coordinator doesn't know this run (it restarted and lost its
			// in-memory queue/state). Replay the stored dispatch payload so a
			// queued run actually runs instead of being silently finalized as
			// "completed" with zero metrics. Only if replay is impossible do we
			// finalize — and then honestly: "completed" only when metrics exist,
			// else "aborted".
			if s.replayDispatch(lctx, r.ID) {
				s.log.Info("reaper: replayed run to restarted coordinator", "run", r.ID)
				continue
			}
			s.finalizeForgotten(r.ID)
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

// replayDispatch re-sends a run's stored StartRun payload to the coordinator
// (StartRun is idempotent on run id), recovering a queued/running run after a
// coordinator restart. Returns true if it was replayed.
func (s *Server) replayDispatch(ctx context.Context, runID string) bool {
	payload, err := s.pg.GetRunDispatch(ctx, runID)
	if err != nil || len(payload) == 0 {
		return false
	}
	var req loadifyv1.StartRunRequest
	if err := proto.Unmarshal(payload, &req); err != nil {
		s.log.Warn("reaper: bad dispatch payload", "run", runID, "err", err)
		return false
	}
	resp, err := s.coord.StartRun(ctx, &req)
	if err != nil {
		s.log.Warn("reaper: replay dispatch failed", "run", runID, "err", err)
		return false
	}
	// Reflect the post-replay status; a running run also gets a fresh watcher.
	if resp != nil && resp.Status == "running" {
		_ = s.pg.SetRunRunning(ctx, runID)
		go s.watchRun(runID, plan.AlertConfig{})
	} else {
		_ = s.pg.SetRunStatus(ctx, runID, "queued")
	}
	return true
}

// finalizeForgotten finalizes a run the coordinator no longer knows and that
// can't be replayed — honestly: "completed" only if it produced metrics,
// otherwise "aborted" (a never-run queued task must not read as a green result).
func (s *Server) finalizeForgotten(runID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, total, err := s.ch.Summary(ctx, runID); err == nil && total > 0 {
		s.finalizeRun(runID, "completed")
		return
	}
	s.log.Warn("reaper: finalizing forgotten run with no metrics as aborted", "run", runID)
	s.finalizeRun(runID, "aborted")
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
	// Default 100 most-recent runs; callers (e.g. the compare picker) may request
	// a deeper history via ?limit=, capped so a huge value can't hammer the DB.
	limit := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
		if limit > 1000 {
			limit = 1000
		}
	}
	runs, err := s.pg.ListRuns(ctx, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runs == nil {
		runs = []postgres.Run{}
	}
	writeJSON(w, http.StatusOK, runs)
}

// handleRunStats returns whole-table run aggregates for the dashboard KPIs, so
// the numbers are correct regardless of history size (the runs list is capped).
func (s *Server) handleRunStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	st, err := s.pg.RunStats(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var passRate *int
	if st.Scored > 0 {
		// Round to nearest whole percent without importing math.
		p := (st.Passed*100 + st.Scored/2) / st.Scored
		passRate = &p
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total":     st.Total,
		"running":   st.Running,
		"last24h":   st.Last24h,
		"scored":    st.Scored,
		"passed":    st.Passed,
		"pass_rate": passRate,
	})
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	run, err := s.pg.GetRun(ctx, chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	// A queued run: ask the coordinator for live admission state. If it has since
	// been dispatched (the DB status lags until the reaper reconciles), reflect
	// "running" now so the page doesn't show a stale "queued" banner with no live
	// chart; otherwise attach the queue position + rough ETA for the banner.
	if run.Status == "queued" {
		if st, serr := s.coord.GetRunState(ctx, &loadifyv1.RunStateRequest{RunId: run.ID}); serr == nil {
			switch {
			case st.Status == loadifyv1.RunStatus_RUN_STATUS_RUNNING:
				run.Status = "running"
				if run.StartedAt == nil {
					now := time.Now()
					run.StartedAt = &now
				}
				_ = s.pg.SetRunRunning(ctx, run.ID) // persist so the lag closes
			case st.QueuePosition > 0:
				writeJSON(w, http.StatusOK, struct {
					*postgres.Run
					QueuePosition int32 `json:"queue_position,omitempty"`
					QueueETAms    int64 `json:"queue_eta_ms,omitempty"`
				}{run, st.QueuePosition, st.QueueEtaMs})
				return
			}
		}
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
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	// Default the time window to the run's own span, so a run that finished more
	// than 24h ago still renders (a fixed last-24h window would miss it — this
	// was why old runs' charts and run comparisons came up blank).
	to := time.Now()
	from := to.Add(-24 * time.Hour)
	if run, err := s.pg.GetRun(ctx, runID); err == nil {
		from = run.CreatedAt.Add(-time.Minute)
		if run.EndedAt != nil {
			to = run.EndedAt.Add(time.Minute)
		}
	}
	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}
	pts, err := s.ch.QuerySeries(ctx, runID, group, from, to, res)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if pts == nil {
		pts = []store.SeriesPoint{}
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

	// Name the download after the run so it's easy to find; keep an ASCII
	// run-<id> fallback and put the (possibly non-ASCII, e.g. Chinese) name in
	// filename* per RFC 5987 so browsers use the friendly name.
	name := strings.TrimSpace(run.Name)
	if name == "" {
		name = "run-" + runID
	}
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\n', '\r', '\t':
			return '-'
		}
		return r
	}, name)
	shortID := runID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="run-`+shortID+`.csv"; filename*=UTF-8''`+url.PathEscape(name)+`.csv`)
	cw := csv.NewWriter(w)
	// error_rate_pct is a percentage (0-100) to match every on-screen surface
	// (run charts, summary, report all show percent); QuerySeries returns a
	// fraction, so multiply by 100 here.
	_ = cw.Write([]string{"ts", "qps", "error_rate_pct", "p50_ms", "p90_ms", "p95_ms", "p99_ms"})
	for _, p := range pts {
		_ = cw.Write([]string{
			p.TS.UTC().Format(time.RFC3339),
			strconv.FormatFloat(p.RPS, 'f', 2, 64),
			strconv.FormatFloat(p.ErrorRate*100, 'f', 4, 64),
			strconv.FormatFloat(p.P50ms, 'f', 2, 64),
			strconv.FormatFloat(p.P90ms, 'f', 2, 64),
			strconv.FormatFloat(p.P95ms, 'f', 2, 64),
			strconv.FormatFloat(p.P99ms, 'f', 2, 64),
		})
	}
	cw.Flush()
}

// handleRunSamples returns sampled request detail for post-run error
// drill-down, optionally filtered by group / status_class / error_kind. The
// detail is sampled (bounded, error-prioritized), not every request.
func (s *Server) handleRunSamples(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	var from, to time.Time
	if ms, _ := strconv.ParseInt(r.URL.Query().Get("from_ms"), 10, 64); ms > 0 {
		from = time.UnixMilli(ms)
	}
	if ms, _ := strconv.ParseInt(r.URL.Query().Get("to_ms"), 10, 64); ms > 0 {
		to = time.UnixMilli(ms)
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	rows, err := s.ch.QuerySamples(ctx, runID, store.SampleFilter{
		Group:       r.URL.Query().Get("group"),
		StatusClass: r.URL.Query().Get("status_class"),
		ErrorKind:   r.URL.Query().Get("error_kind"),
		Limit:       limit,
		From:        from,
		To:          to,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []store.Sample{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sampled": true, "samples": rows})
}

func (s *Server) handleListWorkers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	resp, err := s.coord.ListWorkers(ctx, &loadifyv1.ListWorkersRequest{})
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	workers := resp.Workers
	if workers == nil {
		// Never serialize a nil slice as JSON null: clients expect an array.
		workers = []*loadifyv1.WorkerInfo{}
	}
	writeJSON(w, http.StatusOK, workers)
}

// handleCapacity reports cluster admission headroom so the start-run form can
// warn the user that a run would queue right now.
func (s *Server) handleCapacity(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	cs, err := s.coord.GetCapacity(ctx, &loadifyv1.CapacityRequest{})
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"max_runs":          cs.MaxRuns,
		"running":           cs.Running,
		"queue_depth":       cs.QueueDepth,
		"workers_total":     cs.WorkersTotal,
		"workers_available": cs.WorkersAvailable,
		"cpu_max_pct":       cs.CpuMaxPct,
		"can_accept":        cs.CanAccept,
	})
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
	case plan.Script, plan.Scenario:
		// Script and scenario runs are protocol-agnostic; UNSPECIFIED lets the
		// scheduler use any healthy worker and the worker selects the script
		// driver (scenarios are compiled to a script at launch).
		return loadifyv1.Protocol_PROTOCOL_UNSPECIFIED
	default:
		return loadifyv1.Protocol_PROTOCOL_UNSPECIFIED
	}
}
