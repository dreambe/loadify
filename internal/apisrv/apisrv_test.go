package apisrv

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/auth"
	"github.com/dreambe/loadify/internal/store"
	"github.com/dreambe/loadify/internal/store/postgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// --- fakes ---

type fakeMeta struct {
	users            map[string]*postgres.User // by email
	usersByID        map[string]*postgres.User // by id (for revocation checks)
	activeRuns       []postgres.Run
	finished         map[string]string // runID -> status
	dueOnce          []postgres.Schedule
	scheduleRun      map[string]string // scheduleID -> runID
	owner            *string           // CreatedBy returned for tests/runs/environments
	lastRunCreatedBy *string           // captured from the most recent CreateRun
	lastRunSource    string
	testPlan         json.RawMessage   // overrides the plan returned by GetTestDefinition
	runOverride      *postgres.Run     // overrides the run returned by GetRun
	dispatch         map[string][]byte // stored StartRun payloads by run id
	runStatus        map[string]string // captured SetRunStatus / SetRunRunning
}

func newFakeMeta() *fakeMeta {
	return &fakeMeta{users: map[string]*postgres.User{}, finished: map[string]string{}, scheduleRun: map[string]string{}, dispatch: map[string][]byte{}, runStatus: map[string]string{}}
}

func (f *fakeMeta) CreateTestDefinition(_ context.Context, _ *postgres.TestDefinition) (string, error) {
	return "test-1", nil
}
func (f *fakeMeta) UpdateTestDefinition(_ context.Context, _ *postgres.TestDefinition) error {
	return nil
}
func (f *fakeMeta) ArchiveTestDefinition(_ context.Context, _ string) error { return nil }
func (f *fakeMeta) GetTestDefinition(_ context.Context, id string) (*postgres.TestDefinition, error) {
	planJSON := json.RawMessage(`{"protocol":"http","http":{"url":"http://x"}}`)
	protocol := "http"
	if len(f.testPlan) > 0 {
		planJSON = f.testPlan
		protocol = "scenario"
	}
	return &postgres.TestDefinition{ID: id, Protocol: protocol, CreatedBy: f.owner, PlanJSON: planJSON}, nil
}
func (f *fakeMeta) ListTestDefinitions(_ context.Context, _ int) ([]postgres.TestDefinition, error) {
	return nil, nil
}
func (f *fakeMeta) CreateRun(_ context.Context, _ string, _ int, _ string, createdBy *string, source string, _ json.RawMessage) (string, error) {
	f.lastRunCreatedBy = createdBy
	f.lastRunSource = source
	return "run-1", nil
}
func (f *fakeMeta) SetRunRunning(_ context.Context, id string) error { f.runStatus[id] = "running"; return nil }
func (f *fakeMeta) SetRunStatus(_ context.Context, id, st string) error {
	f.runStatus[id] = st
	return nil
}
func (f *fakeMeta) FinishRun(_ context.Context, id, st string, _ json.RawMessage) (bool, error) {
	if _, done := f.finished[id]; done {
		return false, nil
	}
	f.finished[id] = st
	return true, nil
}
func (f *fakeMeta) GetRun(_ context.Context, id string) (*postgres.Run, error) {
	if f.runOverride != nil {
		return f.runOverride, nil
	}
	return &postgres.Run{ID: id, TestDefID: "test-1", Status: "running", CreatedBy: f.owner}, nil
}
func (f *fakeMeta) SetRunDispatch(_ context.Context, id string, p []byte) error {
	f.dispatch[id] = p
	return nil
}
func (f *fakeMeta) GetRunDispatch(_ context.Context, id string) ([]byte, error) {
	return f.dispatch[id], nil
}
func (f *fakeMeta) ListRuns(_ context.Context, _ int) ([]postgres.Run, error) { return nil, nil }
func (f *fakeMeta) RunStats(_ context.Context) (postgres.RunStats, error) {
	return postgres.RunStats{}, nil
}
func (f *fakeMeta) ListRunsByTest(_ context.Context, _ string, _ int) ([]postgres.Run, error) {
	return nil, nil
}
func (f *fakeMeta) SetBaseline(_ context.Context, _, _ string) error { return nil }
func (f *fakeMeta) ListActiveRuns(_ context.Context) ([]postgres.Run, error) {
	return f.activeRuns, nil
}
func (f *fakeMeta) GetUserByEmail(_ context.Context, email string) (*postgres.User, error) {
	if u, ok := f.users[email]; ok {
		return u, nil
	}
	return nil, postgres.ErrUserNotFound
}
func (f *fakeMeta) GetUserByID(_ context.Context, id string) (*postgres.User, error) {
	if u, ok := f.usersByID[id]; ok {
		return u, nil
	}
	return &postgres.User{ID: id}, nil
}
func (f *fakeMeta) UpsertFeishuUser(_ context.Context, _, _, _, _ string) (*postgres.User, error) {
	return &postgres.User{ID: "u", Role: "viewer"}, nil
}
func (f *fakeMeta) TouchLogin(_ context.Context, _ string) error                  { return nil }
func (f *fakeMeta) UpdateUserRole(_ context.Context, _, _ string) error           { return nil }
func (f *fakeMeta) SetUserPassword(_ context.Context, _, _ string) error          { return nil }
func (f *fakeMeta) SetUserDisabled(_ context.Context, _ string, _ bool) error     { return nil }
func (f *fakeMeta) DeleteUser(_ context.Context, _ string) error                  { return nil }
func (f *fakeMeta) SetUserWebhooks(_ context.Context, _ string, _ []string) error { return nil }
func (f *fakeMeta) SetUserAPIToken(_ context.Context, id, tokenVal string) error {
	if f.usersByID == nil {
		f.usersByID = map[string]*postgres.User{}
	}
	u := f.usersByID[id]
	if u == nil {
		u = &postgres.User{ID: id}
		f.usersByID[id] = u
	}
	u.APIToken = tokenVal
	return nil
}
func (f *fakeMeta) GetUserByAPIToken(_ context.Context, token string) (*postgres.User, error) {
	for _, u := range f.usersByID {
		if u.APIToken != "" && u.APIToken == token {
			return u, nil
		}
	}
	return nil, postgres.ErrUserNotFound
}
func (f *fakeMeta) ListUsers(_ context.Context, _ int) ([]postgres.User, error)   { return nil, nil }
func (f *fakeMeta) CreateUser(_ context.Context, email, name, role, _ string) (*postgres.User, error) {
	return &postgres.User{ID: "new", Email: email, Name: name, Role: role}, nil
}
func (f *fakeMeta) ListEnvironments(_ context.Context, _ int) ([]postgres.Environment, error) {
	return nil, nil
}
func (f *fakeMeta) GetEnvironment(_ context.Context, id string) (*postgres.Environment, error) {
	return &postgres.Environment{ID: id, Name: "dev", CreatedBy: f.owner, Vars: map[string]string{"base_url": "http://dev.example.com"}}, nil
}
func (f *fakeMeta) CreateEnvironment(_ context.Context, _ string, _ map[string]string, _ *string) (string, error) {
	return "env-1", nil
}
func (f *fakeMeta) UpdateEnvironment(_ context.Context, _, _ string, _ map[string]string) error {
	return nil
}
func (f *fakeMeta) DeleteEnvironment(_ context.Context, _ string) error       { return nil }
func (f *fakeMeta) WriteAudit(_ context.Context, _ postgres.AuditEntry) error { return nil }
func (f *fakeMeta) ListAudit(_ context.Context, _ int) ([]postgres.AuditEntry, error) {
	return nil, nil
}
func (f *fakeMeta) CreateSchedule(_ context.Context, _ string, _, _ int, _ *string) (string, error) {
	return "sched-1", nil
}
func (f *fakeMeta) GetSchedule(_ context.Context, id string) (*postgres.Schedule, error) {
	return &postgres.Schedule{ID: id, TestDefID: "test-1", CreatedBy: f.owner}, nil
}
func (f *fakeMeta) ListSchedules(_ context.Context, _ int) ([]postgres.Schedule, error) {
	return nil, nil
}
func (f *fakeMeta) SetScheduleEnabled(_ context.Context, _ string, _ bool) error { return nil }
func (f *fakeMeta) UpdateSchedule(_ context.Context, _ string, _, _ int) error   { return nil }
func (f *fakeMeta) DeleteSchedule(_ context.Context, _ string) error             { return nil }
func (f *fakeMeta) ClaimDueSchedule(_ context.Context) (*postgres.Schedule, error) {
	if len(f.dueOnce) == 0 {
		return nil, nil
	}
	sc := f.dueOnce[0]
	f.dueOnce = f.dueOnce[1:]
	return &sc, nil
}
func (f *fakeMeta) SetScheduleLastRun(_ context.Context, id, runID string) error {
	f.scheduleRun[id] = runID
	return nil
}

type fakeMetrics struct{}

func (fakeMetrics) Summary(_ context.Context, _ string) (store.SeriesPoint, int64, error) {
	return store.SeriesPoint{P95ms: 50}, 100, nil
}
func (fakeMetrics) QuerySeries(_ context.Context, _, _ string, _, _ time.Time, _ int) ([]store.SeriesPoint, error) {
	return nil, nil
}
func (fakeMetrics) QuerySamples(_ context.Context, _ string, _ store.SampleFilter) ([]store.Sample, error) {
	return nil, nil
}

// fakeCoord implements loadifyv1.CoordinatorServiceClient; only the methods used
// by the tests do anything.
type fakeCoord struct {
	loadifyv1.CoordinatorServiceClient
	getState func() (*loadifyv1.RunState, error)
}

func (c *fakeCoord) StartRun(context.Context, *loadifyv1.StartRunRequest, ...grpc.CallOption) (*loadifyv1.StartRunResponse, error) {
	return &loadifyv1.StartRunResponse{RunId: "run-1", AssignedWorkers: 1}, nil
}
func (c *fakeCoord) GetRunState(context.Context, *loadifyv1.RunStateRequest, ...grpc.CallOption) (*loadifyv1.RunState, error) {
	if c.getState != nil {
		return c.getState()
	}
	return nil, status.Error(codes.NotFound, "unknown")
}
func (c *fakeCoord) StreamLive(context.Context, *loadifyv1.LiveRequest, ...grpc.CallOption) (loadifyv1.CoordinatorService_StreamLiveClient, error) {
	return nil, status.Error(codes.Unavailable, "no stream in tests")
}
func (c *fakeCoord) StopRun(context.Context, *loadifyv1.StopRunRequest, ...grpc.CallOption) (*loadifyv1.StopRunResponse, error) {
	return &loadifyv1.StopRunResponse{}, nil
}
func (c *fakeCoord) GetCapacity(context.Context, *loadifyv1.CapacityRequest, ...grpc.CallOption) (*loadifyv1.CapacitySnapshot, error) {
	return &loadifyv1.CapacitySnapshot{MaxRuns: 8, Running: 0, WorkersTotal: 1, WorkersAvailable: 1, CpuMaxPct: 85, CanAccept: true}, nil
}

func newTestServer(meta *fakeMeta, coord loadifyv1.CoordinatorServiceClient) *Server {
	return New(Config{
		Postgres:    meta,
		ClickHouse:  fakeMetrics{},
		Coordinator: coord,
		JWTSecret:   "test-secret",
	})
}

func token(t *testing.T, role auth.Role) string {
	t.Helper()
	tok, err := auth.Issue(auth.Claims{Subject: "u", Email: "u@x", Role: role}, "test-secret", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// --- tests ---

func TestRBACGating(t *testing.T) {
	srv := newTestServer(newFakeMeta(), &fakeCoord{})
	h := srv.Handler()

	do := func(method, path, tok, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	if c := do("GET", "/api/v1/tests", "", ""); c != http.StatusUnauthorized {
		t.Errorf("no token: got %d want 401", c)
	}
	if c := do("GET", "/api/v1/tests", token(t, auth.RoleViewer), ""); c != http.StatusOK {
		t.Errorf("viewer GET: got %d want 200", c)
	}
	validTest := `{"name":"t","protocol":"http","plan":{"protocol":"http","http":{"url":"http://x"}},"ramp":[]}`
	if c := do("POST", "/api/v1/tests", token(t, auth.RoleViewer), validTest); c != http.StatusForbidden {
		t.Errorf("viewer POST: got %d want 403", c)
	}
	if c := do("POST", "/api/v1/tests", token(t, auth.RoleOperator), validTest); c != http.StatusCreated {
		t.Errorf("operator POST: got %d want 201", c)
	}
	// Admin-only route.
	if c := do("GET", "/api/v1/users", token(t, auth.RoleOperator), ""); c != http.StatusForbidden {
		t.Errorf("operator users: got %d want 403", c)
	}
}

// TestOwnershipGating verifies the "shared read, owner-or-admin write" policy:
// an operator cannot mutate resources they don't own, an admin can mutate any,
// and the creator can mutate their own.
func TestOwnershipGating(t *testing.T) {
	meta := newFakeMeta() // owner nil → only an admin may mutate
	srv := newTestServer(meta, &fakeCoord{})
	h := srv.Handler()
	do := func(method, path, tok, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	validPlan := `{"name":"t","protocol":"http","plan":{"protocol":"http","http":{"url":"http://x"}},"ramp":[]}`
	opTok := token(t, auth.RoleOperator) // Subject "u"
	adminTok := token(t, auth.RoleAdmin)

	cases := []struct{ method, path, body string }{
		{"PUT", "/api/v1/tests/test-1", validPlan},
		{"DELETE", "/api/v1/tests/test-1", ""},
		{"POST", "/api/v1/runs/run-1/stop", ""},
		{"DELETE", "/api/v1/environments/env-1", ""},
		{"PUT", "/api/v1/schedules/sched-1", `{"interval_minutes":5,"desired_workers":1}`},
		{"DELETE", "/api/v1/schedules/sched-1", ""},
		{"POST", "/api/v1/schedules/sched-1/enabled?enabled=false", ""},
	}
	for _, tc := range cases {
		// Non-owner operator is forbidden.
		if c := do(tc.method, tc.path, opTok, tc.body); c != http.StatusForbidden {
			t.Errorf("non-owner %s %s: got %d want 403", tc.method, tc.path, c)
		}
		// Admin may mutate anything (and must actually succeed, not 5xx).
		if c := do(tc.method, tc.path, adminTok, tc.body); c/100 != 2 {
			t.Errorf("admin %s %s: got %d, want 2xx", tc.method, tc.path, c)
		}
	}

	// When the operator IS the creator, the same mutations are allowed.
	uid := "u"
	meta.owner = &uid
	for _, tc := range cases {
		if c := do(tc.method, tc.path, opTok, tc.body); c/100 != 2 {
			t.Errorf("owner %s %s: got %d, want 2xx", tc.method, tc.path, c)
		}
	}
}

func TestTokenRevocation(t *testing.T) {
	meta := newFakeMeta()
	meta.usersByID = map[string]*postgres.User{}
	srv := newTestServer(meta, &fakeCoord{})

	do := func(tok string) int {
		req := httptest.NewRequest("GET", "/api/v1/tests", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		return rr.Code
	}

	tok := token(t, auth.RoleViewer) // subject "u", issued now
	if c := do(tok); c != http.StatusOK {
		t.Fatalf("baseline: got %d want 200", c)
	}

	// Disabled account: its still-unexpired token must be rejected.
	meta.usersByID["u"] = &postgres.User{ID: "u", Disabled: true}
	srv.revCache.Delete("u")
	if c := do(tok); c != http.StatusUnauthorized {
		t.Errorf("disabled account: got %d want 401", c)
	}

	// Credentials changed after the token was issued → revoked.
	meta.usersByID["u"] = &postgres.User{ID: "u", CredsChangedAt: time.Now().Add(time.Hour)}
	srv.revCache.Delete("u")
	if c := do(tok); c != http.StatusUnauthorized {
		t.Errorf("creds changed: got %d want 401", c)
	}
}

func TestCreateTestRejectsInvalidPlan(t *testing.T) {
	srv := newTestServer(newFakeMeta(), &fakeCoord{})
	body := `{"name":"t","protocol":"http","plan":{"protocol":"http"},"ramp":[]}` // missing http.url
	req := httptest.NewRequest("POST", "/api/v1/tests", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token(t, auth.RoleOperator))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid plan: got %d want 400", rr.Code)
	}
}

func TestLoginFlow(t *testing.T) {
	meta := newFakeMeta()
	hash, _ := auth.HashPassword("pw12345")
	meta.users["a@b.com"] = &postgres.User{ID: "u1", Email: "a@b.com", Role: "operator", PasswordHash: hash}
	srv := newTestServer(meta, &fakeCoord{})

	bad := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"email":"a@b.com","password":"wrong"}`))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, bad)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("bad login: got %d want 401", rr.Code)
	}

	ok := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"email":"a@b.com","password":"pw12345"}`))
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, ok)
	if rr.Code != http.StatusOK {
		t.Fatalf("good login: got %d want 200", rr.Code)
	}
	var resp tokenResp
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || resp.Token == "" {
		t.Fatalf("expected token, body=%s", rr.Body.String())
	}
}

func TestReaperFinalizesOrphans(t *testing.T) {
	meta := newFakeMeta()
	old := time.Now().Add(-2 * time.Minute)
	fresh := time.Now()
	meta.activeRuns = []postgres.Run{
		{ID: "orphan", Status: "running", CreatedAt: old},     // coordinator unknown -> finalize
		{ID: "brandnew", Status: "running", CreatedAt: fresh}, // too fresh -> skip
	}
	// Coordinator doesn't know either run.
	srv := newTestServer(meta, &fakeCoord{})
	srv.reapOnce(context.Background(), 6*time.Hour)

	if meta.finished["orphan"] != "completed" {
		t.Errorf("orphan not finalized: %v", meta.finished)
	}
	if _, done := meta.finished["brandnew"]; done {
		t.Error("fresh run should not be finalized yet")
	}
}

// TestReaperReplaysForgottenQueuedRun verifies the durable-queue recovery: a
// queued run the (restarted) coordinator no longer knows is replayed from its
// stored dispatch payload — not silently finalized as "completed".
func TestReaperReplaysForgottenQueuedRun(t *testing.T) {
	meta := newFakeMeta()
	old := time.Now().Add(-2 * time.Minute)
	meta.activeRuns = []postgres.Run{{ID: "q1", Status: "queued", CreatedAt: old}}
	payload, _ := proto.Marshal(&loadifyv1.StartRunRequest{RunId: "q1", Protocol: loadifyv1.Protocol_PROTOCOL_HTTP})
	meta.dispatch["q1"] = payload
	// Coordinator doesn't know the run (returns NotFound via default getState).
	srv := newTestServer(meta, &fakeCoord{})
	srv.reapOnce(context.Background(), 6*time.Hour)

	if _, done := meta.finished["q1"]; done {
		t.Errorf("forgotten queued run was finalized instead of replayed: %v", meta.finished)
	}
	if meta.runStatus["q1"] == "" {
		t.Error("replayed run status was not set")
	}
}

func TestSchedulerFiresDueSchedule(t *testing.T) {
	meta := newFakeMeta()
	owner := "alice"
	meta.dueOnce = []postgres.Schedule{{ID: "sched-1", TestDefID: "test-1", IntervalMin: 5, CreatedBy: &owner}}
	srv := newTestServer(meta, &fakeCoord{})

	srv.fireDueSchedules(context.Background())

	if meta.scheduleRun["sched-1"] != "run-1" {
		t.Errorf("schedule did not launch a run: %v", meta.scheduleRun)
	}
	// The run is owned by the schedule's creator but tagged as schedule-triggered.
	if meta.lastRunCreatedBy == nil || *meta.lastRunCreatedBy != "alice" {
		t.Errorf("scheduled run created_by = %v, want alice", meta.lastRunCreatedBy)
	}
	if meta.lastRunSource != "schedule" {
		t.Errorf("scheduled run source = %q, want schedule", meta.lastRunSource)
	}
}

func TestReaperLeavesRunningRun(t *testing.T) {
	meta := newFakeMeta()
	meta.activeRuns = []postgres.Run{{ID: "live", Status: "running", CreatedAt: time.Now().Add(-time.Minute)}}
	coord := &fakeCoord{getState: func() (*loadifyv1.RunState, error) {
		return &loadifyv1.RunState{RunId: "live", Status: loadifyv1.RunStatus_RUN_STATUS_RUNNING}, nil
	}}
	srv := newTestServer(meta, coord)
	srv.reapOnce(context.Background(), 6*time.Hour)
	if _, done := meta.finished["live"]; done {
		t.Error("a still-running run should not be finalized")
	}
}

// TestListEndpointsReturnArrays guards against nil slices marshaling as JSON
// null, which crashes frontend code that calls .length/.map on the response.
func TestListEndpointsReturnArrays(t *testing.T) {
	srv := newTestServer(newFakeMeta(), &fakeCoord{})
	h := srv.Handler()
	tok := token(t, auth.RoleAdmin)

	for _, path := range []string{"/api/v1/tests", "/api/v1/runs", "/api/v1/schedules", "/api/v1/users"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("%s: got %d want 200", path, rr.Code)
			continue
		}
		var v any
		if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
			t.Errorf("%s: invalid json: %v", path, err)
			continue
		}
		if _, ok := v.([]any); !ok {
			t.Errorf("%s: body = %s, want a JSON array", path, rr.Body.String())
		}
	}
}

// TestUserManagementGuards covers the admin user-management endpoints: RBAC,
// self-lockout protection, and the self-service password change.
func TestUserManagementGuards(t *testing.T) {
	srv := newTestServer(newFakeMeta(), &fakeCoord{})
	h := srv.Handler()

	do := func(method, path, tok, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	adm := token(t, auth.RoleAdmin) // subject "u"
	// An admin cannot lock themselves out.
	if c := do("PATCH", "/api/v1/users/u", adm, `{"disabled":true}`); c != http.StatusBadRequest {
		t.Errorf("self-disable: got %d want 400", c)
	}
	if c := do("PATCH", "/api/v1/users/u", adm, `{"role":"viewer"}`); c != http.StatusBadRequest {
		t.Errorf("self-demote: got %d want 400", c)
	}
	if c := do("DELETE", "/api/v1/users/u", adm, ""); c != http.StatusBadRequest {
		t.Errorf("self-delete: got %d want 400", c)
	}
	// Managing another account works.
	if c := do("PATCH", "/api/v1/users/other", adm, `{"role":"operator","disabled":true}`); c != http.StatusOK {
		t.Errorf("patch other: got %d want 200", c)
	}
	if c := do("DELETE", "/api/v1/users/other", adm, ""); c != http.StatusNoContent {
		t.Errorf("delete other: got %d want 204", c)
	}
	// Non-admins are rejected.
	if c := do("PATCH", "/api/v1/users/other", token(t, auth.RoleOperator), `{"role":"viewer"}`); c != http.StatusForbidden {
		t.Errorf("operator patch: got %d want 403", c)
	}
	// Anyone signed in can rotate their own password; short ones are rejected.
	if c := do("POST", "/api/v1/auth/password", token(t, auth.RoleViewer), `{"new_password":"longenough1"}`); c != http.StatusNoContent {
		t.Errorf("change password: got %d want 204", c)
	}
	if c := do("POST", "/api/v1/auth/password", token(t, auth.RoleViewer), `{"new_password":"short"}`); c != http.StatusBadRequest {
		t.Errorf("short password: got %d want 400", c)
	}
}

// TestDebugRequest exercises the test-builder debug endpoint against a real
// local HTTP server: the response status/body must round-trip to the caller.
func TestDebugRequest(t *testing.T) {
	// The httptest server binds loopback, which the SSRF guard blocks; disable
	// the guard for this happy-path test (the guard itself is unit-tested in
	// TestBlockInternalDial).
	old := debugDialControl
	debugDialControl = nil
	defer func() { debugDialControl = old }()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer target.Close()

	srv := newTestServer(newFakeMeta(), &fakeCoord{})
	body := `{"method":"GET","url":"` + target.URL + `"}`
	req := httptest.NewRequest("POST", "/api/v1/tests/debug", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token(t, auth.RoleOperator))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("debug: got %d want 200 (%s)", rr.Code, rr.Body.String())
	}
	var resp debugResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != http.StatusTeapot || !strings.Contains(resp.Body, "world") || resp.Error != "" {
		t.Errorf("debug response = %+v", resp)
	}
	// Viewers cannot debug-fire requests.
	req2 := httptest.NewRequest("POST", "/api/v1/tests/debug", strings.NewReader(body))
	req2.Header.Set("Authorization", "Bearer "+token(t, auth.RoleViewer))
	rr2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusForbidden {
		t.Errorf("viewer debug: got %d want 403", rr2.Code)
	}
}
