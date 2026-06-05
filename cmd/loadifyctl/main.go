// Command loadifyctl is a small CLI that drives a load test end-to-end against a
// running apisrv: it authenticates, creates a test definition, starts a run and
// polls for the summary. Handy for CI smoke tests and local use.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	var (
		api      = flag.String("api", "http://localhost:8080", "apisrv base URL")
		token    = flag.String("token", os.Getenv("LOADIFY_TOKEN"), "bearer token (or set LOADIFY_TOKEN)")
		email    = flag.String("email", os.Getenv("LOADIFY_EMAIL"), "login email (if no token)")
		password = flag.String("password", os.Getenv("LOADIFY_PASSWORD"), "login password (if no token)")
		url      = flag.String("url", "", "target URL to load test (http protocol)")
		method   = flag.String("method", "GET", "HTTP method")
		protocol = flag.String("protocol", "http", "protocol: http|https|script")
		script   = flag.String("script", "", "path to a goja JS script (implies protocol=script)")
		vus      = flag.Int("vus", 20, "virtual users")
		dur      = flag.Duration("duration", 15*time.Second, "test duration")
		workers  = flag.Int("workers", 0, "desired workers (0 = all)")
		name     = flag.String("name", "loadifyctl-run", "test name")
	)
	flag.Parse()

	c := &client{base: *api, token: *token, http: &http.Client{Timeout: 30 * time.Second}}

	// Authenticate if no token was supplied directly.
	if c.token == "" && *email != "" {
		tok, err := c.login(*email, *password)
		must(err)
		c.token = tok
	}

	proto := *protocol
	var scriptJS string
	if *script != "" {
		proto = "script"
		b, err := os.ReadFile(*script)
		must(err)
		scriptJS = string(b)
	}

	planJSON := map[string]any{"protocol": proto}
	switch proto {
	case "http", "https":
		if *url == "" {
			fmt.Fprintln(os.Stderr, "error: --url is required for http/https")
			os.Exit(2)
		}
		planJSON["http"] = map[string]any{"method": *method, "url": *url}
	case "script":
		// no target config; the script generates traffic
	default:
		fmt.Fprintf(os.Stderr, "error: --protocol %q not supported by loadifyctl (use the API/UI)\n", proto)
		os.Exit(2)
	}
	rampJSON := []map[string]any{{"duration_ms": dur.Milliseconds(), "target_vus": *vus}}

	testID, err := c.createTest(*name, proto, planJSON, rampJSON, scriptJS)
	must(err)
	fmt.Printf("created test %s\n", testID)

	runID, err := c.startRun(testID, *workers)
	must(err)
	fmt.Printf("started run %s; waiting %s ...\n", runID, *dur)

	deadline := time.Now().Add(*dur + 30*time.Second)
	for time.Now().Before(deadline) {
		run, err := c.getRun(runID)
		must(err)
		if run.Status == "completed" || run.Status == "failed" {
			out, _ := json.MarshalIndent(run, "", "  ")
			fmt.Printf("run %s:\n%s\n", run.Status, out)
			if run.Status == "failed" {
				os.Exit(1)
			}
			return
		}
		time.Sleep(2 * time.Second)
	}
	fmt.Fprintln(os.Stderr, "timed out waiting for run")
	os.Exit(1)
}

type client struct {
	base  string
	token string
	http  *http.Client
}

func (c *client) login(email, password string) (string, error) {
	var resp struct {
		Token string `json:"token"`
	}
	if err := c.post("/api/v1/auth/login", map[string]any{"email": email, "password": password}, &resp); err != nil {
		return "", err
	}
	if resp.Token == "" {
		return "", fmt.Errorf("login returned no token")
	}
	return resp.Token, nil
}

func (c *client) createTest(name, proto string, plan any, ramp any, scriptJS string) (string, error) {
	planB, _ := json.Marshal(plan)
	rampB, _ := json.Marshal(ramp)
	body := map[string]any{
		"name":     name,
		"protocol": proto,
		"plan":     json.RawMessage(planB),
		"ramp":     json.RawMessage(rampB),
	}
	if scriptJS != "" {
		body["script"] = scriptJS
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := c.post("/api/v1/tests", body, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

func (c *client) startRun(testID string, workers int) (string, error) {
	var resp struct {
		RunID string `json:"run_id"`
	}
	if err := c.post("/api/v1/runs", map[string]any{"test_id": testID, "desired_workers": workers}, &resp); err != nil {
		return "", err
	}
	return resp.RunID, nil
}

type runView struct {
	ID      string          `json:"id"`
	Status  string          `json:"status"`
	Summary json.RawMessage `json:"summary"`
}

func (c *client) getRun(id string) (*runView, error) {
	var rv runView
	if err := c.get("/api/v1/runs/"+id, &rv); err != nil {
		return nil, err
	}
	return &rv, nil
}

func (c *client) post(path string, body, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

func (c *client) get(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *client) do(req *http.Request, out any) error {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	return decode(resp, out)
}

func decode(resp *http.Response, out any) error {
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, string(data))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
