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
)

// --- fakes ---

type fakeMeta struct {
	users      map[string]*postgres.User // by email
	activeRuns []postgres.Run
	finished   map[string]string // runID -> status
	dueOnce     []postgres.Schedule
	scheduleRun map[string]string // scheduleID -> runID
}

func newFakeMeta() *fakeMeta {
	return &fakeMeta{users: map[string]*postgres.User{}, finished: map[string]string{}, scheduleRun: map[string]string{}}
}

func (f *fakeMeta) CreateTestDefinition(_ context.Context, _ *postgres.TestDefinition) (string, error) {
	return "test-1", nil
}
func (f *fakeMeta) UpdateTestDefinition(_ context.Context, _ *postgres.TestDefinition) error {
	return nil
}
func (f *fakeMeta) ArchiveTestDefinition(_ context.Context, _ string) error { return nil }
func (f *fakeMeta) GetTestDefinition(_ context.Context, id string) (*postgres.TestDefinition, error) {
	return &postgres.TestDefinition{ID: id, Protocol: "http", PlanJSON: json.RawMessage(`{"protocol":"http","http":{"url":"http://x"}}`)}, nil
}
func (f *fakeMeta) ListTestDefinitions(_ context.Context, _ int) ([]postgres.TestDefinition, error) {
	return nil, nil
}
func (f *fakeMeta) CreateRun(_ context.Context, _ string, _ int, _ string, _ *string, _ json.RawMessage) (string, error) {
	return "run-1", nil
}
func (f *fakeMeta) SetRunRunning(_ context.Context, _ string) error              { return nil }
func (f *fakeMeta) SetRunStatus(_ context.Context, _, _ string) error            { return nil }
func (f *fakeMeta) FinishRun(_ context.Context, id, st string, _ json.RawMessage) (bool, error) {
	if _, done := f.finished[id]; done {
		return false, nil
	}
	f.finished[id] = st
	return true, nil
}
func (f *fakeMeta) GetRun(_ context.Context, id string) (*postgres.Run, error) {
	return &postgres.Run{ID: id, TestDefID: "test-1", Status: "running"}, nil
}
func (f *fakeMeta) ListRuns(_ context.Context, _ int) ([]postgres.Run, error)      { return nil, nil }
func (f *fakeMeta) ListActiveRuns(_ context.Context) ([]postgres.Run, error)       { return f.activeRuns, nil }
func (f *fakeMeta) GetUserByEmail(_ context.Context, email string) (*postgres.User, error) {
	if u, ok := f.users[email]; ok {
		return u, nil
	}
	return nil, postgres.ErrUserNotFound
}
func (f *fakeMeta) GetUserByID(_ context.Context, id string) (*postgres.User, error) {
	return &postgres.User{ID: id}, nil
}
func (f *fakeMeta) UpsertFeishuUser(_ context.Context, _, _, _, _ string) (*postgres.User, error) {
	return &postgres.User{ID: "u", Role: "viewer"}, nil
}
func (f *fakeMeta) TouchLogin(_ context.Context, _ string) error { return nil }
func (f *fakeMeta) UpdateUserRole(_ context.Context, _, _ string) error       { return nil }
func (f *fakeMeta) SetUserPassword(_ context.Context, _, _ string) error      { return nil }
func (f *fakeMeta) SetUserDisabled(_ context.Context, _ string, _ bool) error { return nil }
func (f *fakeMeta) DeleteUser(_ context.Context, _ string) error              { return nil }
func (f *fakeMeta) SetUserWebhooks(_ context.Context, _ string, _ []string) error { return nil }
func (f *fakeMeta) ListUsers(_ context.Context, _ int) ([]postgres.User, error) { return nil, nil }
func (f *fakeMeta) CreateUser(_ context.Context, email, name, role, _ string) (*postgres.User, error) {
	return &postgres.User{ID: "new", Email: email, Name: name, Role: role}, nil
}
func (f *fakeMeta) CreateSchedule(_ context.Context, _ string, _, _ int) (string, error) {
	return "sched-1", nil
}
func (f *fakeMeta) ListSchedules(_ context.Context, _ int) ([]postgres.Schedule, error) { return nil, nil }
func (f *fakeMeta) SetScheduleEnabled(_ context.Context, _ string, _ bool) error        { return nil }
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
		{ID: "orphan", Status: "running", CreatedAt: old},   // coordinator unknown -> finalize
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

func TestSchedulerFiresDueSchedule(t *testing.T) {
	meta := newFakeMeta()
	meta.dueOnce = []postgres.Schedule{{ID: "sched-1", TestDefID: "test-1", IntervalMin: 5}}
	srv := newTestServer(meta, &fakeCoord{})

	srv.fireDueSchedules(context.Background())

	if meta.scheduleRun["sched-1"] != "run-1" {
		t.Errorf("schedule did not launch a run: %v", meta.scheduleRun)
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
