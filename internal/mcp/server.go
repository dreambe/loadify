// Package mcp implements a minimal Model Context Protocol server (stdio,
// JSON-RPC 2.0) that exposes loadify as agent-callable tools: an agent can
// create and run load tests and read results without the UI. It speaks the
// initialize / tools/list / tools/call subset of MCP over newline-delimited
// JSON, backed by the loadify REST client.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/dreambe/loadify/internal/apiclient"
)

const protocolVersion = "2024-11-05"

// Server dispatches MCP requests to loadify API calls.
type Server struct {
	client *apiclient.Client
}

// NewServer builds an MCP server over a loadify API client.
func NewServer(c *apiclient.Client) *Server { return &Server{client: c} }

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve reads JSON-RPC messages from in and writes responses to out until EOF.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
			continue
		}
		// Notifications (no id) get no response.
		if len(req.ID) == 0 {
			continue
		}
		result, rerr := s.handle(ctx, req.Method, req.Params)
		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
		if rerr != nil {
			resp.Error = rerr
		} else {
			resp.Result = result
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (s *Server) handle(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "loadify", "version": "0.1.0"},
		}, nil
	case "tools/list":
		return map[string]any{"tools": toolDefs()}, nil
	case "tools/call":
		return s.callTool(ctx, params)
	case "ping":
		return map[string]any{}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
	}
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p callParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	}
	text, err := s.dispatch(ctx, p.Name, p.Arguments)
	if err != nil {
		// Tool errors are reported as results with isError so the agent sees them.
		return toolResult(err.Error(), true), nil
	}
	return toolResult(text, false), nil
}

func toolResult(text string, isErr bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isErr,
	}
}

func (s *Server) dispatch(ctx context.Context, name string, args json.RawMessage) (string, error) {
	switch name {
	case "loadify_quick_run":
		return s.quickRun(ctx, args)
	case "loadify_run_status":
		return s.runStatus(ctx, args)
	case "loadify_list_workers":
		return s.listWorkers(ctx)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

type quickRunArgs struct {
	Name            string `json:"name"`
	Protocol        string `json:"protocol"`
	Method          string `json:"method"`
	URL             string `json:"url"`
	Script          string `json:"script"`
	VUs             int    `json:"vus"`
	TargetRPS       int    `json:"target_rps"`
	DurationSeconds int    `json:"duration_seconds"`
	Workers         int    `json:"workers"`
	Wait            *bool  `json:"wait"`
}

func (s *Server) quickRun(ctx context.Context, raw json.RawMessage) (string, error) {
	var a quickRunArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if a.Protocol == "" {
		a.Protocol = "http"
	}
	if a.Method == "" {
		a.Method = "GET"
	}
	if a.DurationSeconds <= 0 {
		a.DurationSeconds = 30
	}
	if a.VUs <= 0 && a.TargetRPS <= 0 {
		a.VUs = 20
	}
	if a.Name == "" {
		a.Name = "mcp-run"
	}

	plan, err := buildPlan(a.Protocol, a.Method, a.URL)
	if err != nil {
		return "", err
	}
	ramp := buildRamp(a.DurationSeconds, a.VUs, a.TargetRPS)

	testID, err := s.client.CreateTest(ctx, apiclient.CreateTestRequest{
		Name: a.Name, Protocol: a.Protocol, Plan: plan, Ramp: ramp, Script: a.Script,
	})
	if err != nil {
		return "", err
	}
	runID, err := s.client.StartRun(ctx, testID, a.Workers)
	if err != nil {
		return "", err
	}

	wait := true
	if a.Wait != nil {
		wait = *a.Wait
	}
	if !wait {
		return jsonString(map[string]any{"run_id": runID, "test_id": testID, "status": "running"}), nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(a.DurationSeconds+90)*time.Second)
	defer cancel()
	run, err := s.client.WaitForRun(waitCtx, runID, 2*time.Second)
	if err != nil {
		return jsonString(map[string]any{"run_id": runID, "test_id": testID, "status": "running", "note": "still running: " + err.Error()}), nil
	}
	return jsonString(map[string]any{
		"run_id":  runID,
		"test_id": testID,
		"status":  run.Status,
		"summary": json.RawMessage(orNull(run.Summary)),
	}), nil
}

type runStatusArgs struct {
	RunID string `json:"run_id"`
}

func (s *Server) runStatus(ctx context.Context, raw json.RawMessage) (string, error) {
	var a runStatusArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.RunID == "" {
		return "", fmt.Errorf("run_id is required")
	}
	run, err := s.client.GetRun(ctx, a.RunID)
	if err != nil {
		return "", err
	}
	return jsonString(map[string]any{"run_id": run.ID, "status": run.Status, "summary": json.RawMessage(orNull(run.Summary))}), nil
}

func (s *Server) listWorkers(ctx context.Context) (string, error) {
	ws, err := s.client.ListWorkers(ctx)
	if err != nil {
		return "", err
	}
	return jsonString(map[string]any{"workers": ws, "count": len(ws)}), nil
}

func buildPlan(protocol, method, url string) (any, error) {
	switch protocol {
	case "http", "https":
		if url == "" {
			return nil, fmt.Errorf("url is required for %s", protocol)
		}
		return map[string]any{"protocol": protocol, "http": map[string]any{"method": method, "url": url}}, nil
	case "script":
		return map[string]any{"protocol": "script"}, nil
	default:
		return nil, fmt.Errorf("protocol %q not supported by quick_run (use the REST API for grpc/ws/sse)", protocol)
	}
}

func buildRamp(durationSeconds, vus, rps int) []map[string]any {
	if rps > 0 {
		// Open model: quick ramp to the target rate, then hold.
		ramp := int(0.2 * float64(durationSeconds) * 1000)
		if ramp < 200 {
			ramp = 200
		}
		hold := durationSeconds*1000 - ramp
		if hold < 0 {
			hold = durationSeconds * 1000
		}
		return []map[string]any{
			{"duration_ms": ramp, "target_rps": rps},
			{"duration_ms": hold, "target_rps": rps},
		}
	}
	return []map[string]any{{"duration_ms": durationSeconds * 1000, "target_vus": vus}}
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func orNull(r json.RawMessage) string {
	if len(r) == 0 {
		return "null"
	}
	return string(r)
}
