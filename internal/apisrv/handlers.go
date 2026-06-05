package apisrv

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/store/postgres"
	"github.com/go-chi/chi/v5"
)

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
	Name     string          `json:"name"`
	Protocol string          `json:"protocol"`
	Plan     json.RawMessage `json:"plan"`
	Ramp     json.RawMessage `json:"ramp"`
	Script   string          `json:"script,omitempty"`
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
		Name:     req.Name,
		Protocol: req.Protocol,
		PlanJSON:  req.Plan,
		RampJSON:  req.Ramp,
		ScriptJS:  req.Script,
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

	td, err := s.pg.GetTestDefinition(ctx, req.TestID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "test not found")
		return
	}
	p, err := plan.Parse(td.PlanJSON)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ramp, err := parseRamp(td.RampJSON)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	runID, err := s.pg.CreateRun(ctx, td.ID, req.DesiredWorkers)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	var script *loadifyv1.ScriptBundle
	if td.ScriptJS != "" {
		script = &loadifyv1.ScriptBundle{MainJs: td.ScriptJS}
	}
	_, err = s.coord.StartRun(ctx, &loadifyv1.StartRunRequest{
		RunId:          runID,
		Protocol:       protoEnum(p.Protocol),
		PlanJson:       td.PlanJSON,
		Ramp:           ramp,
		Script:         script,
		DesiredWorkers: int32(req.DesiredWorkers),
	})
	if err != nil {
		_ = s.pg.FinishRun(context.Background(), runID, "failed", json.RawMessage(`{"error":"dispatch failed"}`))
		writeErr(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	_ = s.pg.SetRunRunning(ctx, runID)

	// Watch the run to completion and persist a summary.
	go s.watchRun(runID)

	writeJSON(w, http.StatusAccepted, map[string]string{"run_id": runID, "status": "running"})
}

// watchRun blocks on the live stream; when it closes the run is finished, so we
// compute the summary from the metric store and mark the run complete.
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
	sctx, scancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer scancel()
	summary, total, serr := s.ch.Summary(sctx, runID)
	status := "completed"
	payload := map[string]any{"total_requests": total, "summary": summary}
	if serr != nil {
		s.log.Warn("run summary failed", "run", runID, "err", serr)
	}
	body, _ := json.Marshal(payload)
	if err := s.pg.FinishRun(sctx, runID, status, body); err != nil {
		s.log.Warn("finish run failed", "run", runID, "err", err)
	}
	s.log.Info("run finalized", "run", runID, "total", total)
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
	default:
		return loadifyv1.Protocol_PROTOCOL_UNSPECIFIED
	}
}
