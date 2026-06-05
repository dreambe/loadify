package script

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// bindHTTP injects an `http` object exposing get/post/request. Every call folds
// its timing, byte counts and status into the iteration accumulator and returns
// a JS object: { status, ok, duration_ms, body, error }.
func bindHTTP(rt *goja.Runtime, client *http.Client, acc *accumulator) error {
	obj := rt.NewObject()

	do := func(method, url, body string, params map[string]string) goja.Value {
		return rt.ToValue(doRequest(rt, client, acc, method, url, body, params))
	}

	_ = obj.Set("request", func(call goja.FunctionCall) goja.Value {
		method := strings.ToUpper(call.Argument(0).String())
		url := call.Argument(1).String()
		body := optString(call.Argument(2))
		return do(method, url, body, optParams(rt, call.Argument(3)))
	})
	_ = obj.Set("get", func(call goja.FunctionCall) goja.Value {
		return do(http.MethodGet, call.Argument(0).String(), "", optParams(rt, call.Argument(1)))
	})
	_ = obj.Set("post", func(call goja.FunctionCall) goja.Value {
		return do(http.MethodPost, call.Argument(0).String(), optString(call.Argument(1)), optParams(rt, call.Argument(2)))
	})

	return rt.Set("http", obj)
}

type httpResult struct {
	Status     int    `json:"status"`
	OK         bool   `json:"ok"`
	DurationMs int64  `json:"duration_ms"`
	Body       string `json:"body"`
	Error      string `json:"error"`
}

func doRequest(rt *goja.Runtime, client *http.Client, acc *accumulator, method, url, body string, headers map[string]string) httpResult {
	acc.calls++
	if method == "" {
		method = http.MethodGet
	}

	ctx := acc.ctx
	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
		acc.sent += int64(len(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		acc.failed = true
		acc.errKind = "build_request"
		return httpResult{Error: err.Error()}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		acc.failed = true
		acc.errKind = classifyErr(err)
		acc.latencyUs += time.Since(start).Microseconds()
		return httpResult{Error: err.Error()}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	dur := time.Since(start)

	acc.latencyUs += dur.Microseconds()
	if acc.ttfbUs == 0 {
		acc.ttfbUs = dur.Microseconds()
	}
	acc.recv += int64(len(data))
	acc.status = int32(resp.StatusCode)
	if resp.StatusCode >= 400 {
		acc.failed = true
		if acc.errKind == "" {
			acc.errKind = "http_status"
		}
	}

	return httpResult{
		Status:     resp.StatusCode,
		OK:         resp.StatusCode < 400,
		DurationMs: dur.Milliseconds(),
		Body:       string(data),
	}
}

func optString(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

// optParams reads an optional { headers: {..} } argument, returning header map.
func optParams(rt *goja.Runtime, v goja.Value) map[string]string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	obj := v.ToObject(rt)
	hv := obj.Get("headers")
	if hv == nil || goja.IsUndefined(hv) || goja.IsNull(hv) {
		return nil
	}
	hobj := hv.ToObject(rt)
	out := make(map[string]string)
	for _, k := range hobj.Keys() {
		out[k] = hobj.Get(k).String()
	}
	return out
}

func classifyErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "context deadline exceeded"), strings.Contains(s, "Timeout"):
		return "timeout"
	case strings.Contains(s, "connection refused"):
		return "conn_refused"
	case strings.Contains(s, "no such host"):
		return "dns"
	case strings.Contains(s, "EOF"):
		return "eof"
	default:
		return "transport"
	}
}
