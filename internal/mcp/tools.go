package mcp

// toolDefs returns the MCP tool catalog advertised to agents.
func toolDefs() []map[string]any {
	return []map[string]any{
		{
			"name":        "loadify_quick_run",
			"description": "Create and start a load test in one call, then (by default) wait for it to finish and return the summary. Use for HTTP/HTTPS (give a url) or a goja script (protocol=script, give script). Choose load shape with either vus (closed model) or target_rps (open/arrival-rate model).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":             map[string]any{"type": "string", "description": "test name"},
					"protocol":         map[string]any{"type": "string", "enum": []string{"http", "https", "script"}, "description": "default http"},
					"method":           map[string]any{"type": "string", "description": "HTTP method, default GET"},
					"url":              map[string]any{"type": "string", "description": "target URL (http/https)"},
					"script":           map[string]any{"type": "string", "description": "goja JS defining iteration() (protocol=script)"},
					"vus":              map[string]any{"type": "integer", "description": "virtual users (closed model), default 20"},
					"target_rps":       map[string]any{"type": "integer", "description": "target requests/sec (open model); overrides vus"},
					"duration_seconds": map[string]any{"type": "integer", "description": "test duration, default 30"},
					"workers":          map[string]any{"type": "integer", "description": "desired workers, 0 = all"},
					"wait":             map[string]any{"type": "boolean", "description": "wait for completion and return summary, default true"},
				},
			},
		},
		{
			"name":        "loadify_run_status",
			"description": "Get the status and summary of a run by id.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"run_id": map[string]any{"type": "string"}},
				"required":   []string{"run_id"},
			},
		},
		{
			"name":        "loadify_list_workers",
			"description": "List connected load-generation workers and their status.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}
