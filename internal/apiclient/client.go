// Package apiclient is a small Go client for the loadify apisrv REST API,
// shared by the CLI and the MCP server so humans and agents drive runs the same
// way.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client talks to a loadify apisrv instance.
type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

// New creates a client for the given apisrv base URL.
func New(base, token string) *Client {
	return &Client{Base: base, Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Login exchanges email/password for a JWT and stores it on the client.
func (c *Client) Login(ctx context.Context, email, password string) (string, error) {
	var resp struct {
		Token string `json:"token"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": email, "password": password}, &resp); err != nil {
		return "", err
	}
	if resp.Token == "" {
		return "", fmt.Errorf("apiclient: login returned no token")
	}
	c.Token = resp.Token
	return resp.Token, nil
}

// CreateTestRequest is a declarative test definition.
type CreateTestRequest struct {
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	Plan       any    `json:"plan"`
	Ramp       any    `json:"ramp"`
	Script     string `json:"script,omitempty"`
	Thresholds any    `json:"thresholds,omitempty"`
	Dataset    any    `json:"dataset,omitempty"`
}

// CreateTest stores a test definition and returns its id.
func (c *Client) CreateTest(ctx context.Context, req CreateTestRequest) (string, error) {
	var resp struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/tests", req, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

// StartRun dispatches a run for a test and returns the run id.
func (c *Client) StartRun(ctx context.Context, testID string, workers int) (string, error) {
	var resp struct {
		RunID string `json:"run_id"`
	}
	body := map[string]any{"test_id": testID, "desired_workers": workers}
	if err := c.do(ctx, http.MethodPost, "/api/v1/runs", body, &resp); err != nil {
		return "", err
	}
	return resp.RunID, nil
}

// StopRun asks the coordinator to stop a run.
func (c *Client) StopRun(ctx context.Context, runID string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/runs/"+runID+"/stop", nil, nil)
}

// Run is a run's stored state.
type Run struct {
	ID        string          `json:"id"`
	Status    string          `json:"status"`
	Summary   json.RawMessage `json:"summary"`
	StartedAt *time.Time      `json:"started_at,omitempty"`
	EndedAt   *time.Time      `json:"ended_at,omitempty"`
}

// GetRun fetches a run by id.
func (c *Client) GetRun(ctx context.Context, runID string) (*Run, error) {
	var r Run
	if err := c.do(ctx, http.MethodGet, "/api/v1/runs/"+runID, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Worker mirrors apisrv's worker view.
type Worker struct {
	WorkerID  string `json:"worker_id"`
	Region    string `json:"region"`
	Status    string `json:"status"`
	ActiveVUs int64  `json:"active_vus"`
}

// ListWorkers returns the connected workers.
func (c *Client) ListWorkers(ctx context.Context) ([]Worker, error) {
	var ws []Worker
	if err := c.do(ctx, http.MethodGet, "/api/v1/workers", nil, &ws); err != nil {
		return nil, err
	}
	return ws, nil
}

// WaitForRun polls a run until it reaches a terminal state or ctx is done.
func (c *Client) WaitForRun(ctx context.Context, runID string, poll time.Duration) (*Run, error) {
	if poll <= 0 {
		poll = 2 * time.Second
	}
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		run, err := c.GetRun(ctx, runID)
		if err != nil {
			return nil, err
		}
		switch run.Status {
		case "completed", "failed", "aborted":
			return run, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("apiclient: %s %s: http %d: %s", method, path, resp.StatusCode, string(data))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}
