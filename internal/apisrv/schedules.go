package apisrv

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

type createScheduleReq struct {
	TestID          string `json:"test_id"`
	IntervalMinutes int    `json:"interval_minutes"`
	DesiredWorkers  int    `json:"desired_workers"`
}

func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	var req createScheduleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.TestID == "" || req.IntervalMinutes <= 0 {
		writeErr(w, http.StatusBadRequest, "test_id and a positive interval_minutes are required")
		return
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	if _, err := s.pg.GetTestDefinition(ctx, req.TestID); err != nil {
		writeErr(w, http.StatusNotFound, "test not found")
		return
	}
	id, err := s.pg.CreateSchedule(ctx, req.TestID, req.IntervalMinutes, req.DesiredWorkers)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	scs, err := s.pg.ListSchedules(ctx, 200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, scs)
}

func (s *Server) handleSetScheduleEnabled(w http.ResponseWriter, r *http.Request) {
	enabled := r.URL.Query().Get("enabled") != "false"
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	if err := s.pg.SetScheduleEnabled(ctx, chi.URLParam(r, "id"), enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled})
}

// StartScheduler periodically claims due schedules and launches their runs.
// Claiming is atomic in the store (FOR UPDATE SKIP LOCKED), so running multiple
// apisrv replicas never double-fires a schedule.
func (s *Server) StartScheduler(ctx context.Context, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.fireDueSchedules(ctx)
		}
	}
}

func (s *Server) fireDueSchedules(ctx context.Context) {
	for {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		sc, err := s.pg.ClaimDueSchedule(cctx)
		if err != nil {
			cancel()
			s.log.Warn("scheduler: claim failed", "err", err)
			return
		}
		if sc == nil {
			cancel()
			return // nothing due
		}
		runID, st, err := s.launchRun(cctx, sc.TestDefID, sc.DesiredWorkers)
		if err != nil {
			s.log.Warn("scheduler: launch failed", "schedule", sc.ID, "err", err)
		} else {
			_ = s.pg.SetScheduleLastRun(cctx, sc.ID, runID)
			s.log.Info("scheduler: launched run", "schedule", sc.ID, "run", runID, "status", st)
		}
		cancel()
	}
}
