package apisrv

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/dreambe/loadify/internal/store/postgres"
	"github.com/go-chi/chi/v5"
)

// envVarToken matches {{ KEY }} placeholders with a word-ish key.
var envVarToken = regexp.MustCompile(`\{\{\s*([\w.-]+)\s*\}\}`)

// substituteEnv replaces {{KEY}} placeholders in s with values from vars. Only
// keys present in vars are replaced; any other {{...}} is left intact so the
// scenario runtime (extracts, dataset, built-in generators) can still resolve
// them. This runs over the raw plan JSON and script at launch, so every
// protocol — not just scenarios — gets environment substitution.
func substituteEnv(s string, vars map[string]string) string {
	if s == "" || len(vars) == 0 {
		return s
	}
	return envVarToken.ReplaceAllStringFunc(s, func(m string) string {
		sub := envVarToken.FindStringSubmatch(m)
		if v, ok := vars[strings.TrimSpace(sub[1])]; ok {
			return v
		}
		return m
	})
}

func statusForEnvErr(err error) int {
	if errors.Is(err, postgres.ErrEnvNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

type envReq struct {
	Name string            `json:"name"`
	Vars map[string]string `json:"vars"`
}

func (s *Server) handleListEnvironments(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	envs, err := s.pg.ListEnvironments(ctx, 200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if envs == nil {
		envs = []postgres.Environment{}
	}
	writeJSON(w, http.StatusOK, envs)
}

func (s *Server) handleCreateEnvironment(w http.ResponseWriter, r *http.Request) {
	var req envReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	id, err := s.pg.CreateEnvironment(ctx, req.Name, req.Vars, callerID(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) handleUpdateEnvironment(w http.ResponseWriter, r *http.Request) {
	var req envReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	existing, err := s.pg.GetEnvironment(ctx, chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, statusForEnvErr(err), err.Error())
		return
	}
	if s.denyIfNotOwner(w, r, existing.CreatedBy) {
		return
	}
	if err := s.pg.UpdateEnvironment(ctx, chi.URLParam(r, "id"), req.Name, req.Vars); err != nil {
		writeErr(w, statusForEnvErr(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteEnvironment(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	existing, err := s.pg.GetEnvironment(ctx, chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, statusForEnvErr(err), err.Error())
		return
	}
	if s.denyIfNotOwner(w, r, existing.CreatedBy) {
		return
	}
	if err := s.pg.DeleteEnvironment(ctx, chi.URLParam(r, "id")); err != nil {
		writeErr(w, statusForEnvErr(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
