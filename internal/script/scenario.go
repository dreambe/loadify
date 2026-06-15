package script

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dreambe/loadify/internal/plan"
)

// CompileScenario turns a no-code scenario into a goja script the existing
// script driver runs. The scenario spec is embedded as a JSON literal and a
// fixed harness interprets it: {{var}} interpolation, JSON field extraction
// for chaining, per-step assertions via check(), and weighted step selection.
//
// Keeping the harness fixed (data-driven) rather than code-generating each
// step keeps the emitted script small and the escaping trivially safe — only
// the embedded JSON needs care, and json.Marshal handles that.
func CompileScenario(sc *plan.ScenarioConfig) (string, error) {
	if sc == nil || len(sc.Steps) == 0 {
		return "", fmt.Errorf("script: empty scenario")
	}
	spec, err := json.Marshal(sc)
	if err != nil {
		return "", err
	}
	// U+2028/U+2029 are valid in JSON but terminate JS string literals; escape
	// them so the embedded literal is always valid JS.
	js := strings.NewReplacer("\u2028", "\\u2028", "\u2029", "\\u2029").Replace(string(spec))
	return "var __SCENARIO__ = " + js + ";\n" + scenarioHarness, nil
}

// scenarioHarness is the static interpreter prepended with the spec literal.
const scenarioHarness = `
function _get(o, path) {
  var segs = String(path).split(".");
  for (var i = 0; i < segs.length; i++) {
    if (o === null || o === undefined) return undefined;
    o = o[segs[i]];
  }
  return o;
}
function _tmplFunc(token) {
  // Built-in generators usable directly in {{...}}: uuid, timestamp, now,
  // random, randomInt(a,b). Returns undefined when token is not a function.
  var m = token.match(/^(\w+)\s*\(([^)]*)\)$/);
  var name = m ? m[1] : token;
  var args = m && m[2].trim() ? m[2].split(",").map(function (x) { return parseFloat(x); }) : [];
  switch (name) {
    case "uuid": return uuid();
    case "timestamp": return timestamp();
    case "now": return now();
    case "random": return random();
    case "randomInt": return randomInt(args[0] || 0, args[1] || 0);
  }
  return undefined;
}
function _interp(s, vars) {
  if (s === undefined || s === null) return s;
  return String(s).replace(/\{\{\s*([\w.]+(?:\([^)]*\))?)\s*\}\}/g, function (m, k) {
    var v = vars[k];
    if (v === undefined || v === null) {
      var f = _tmplFunc(k);
      if (f !== undefined) return String(f);
      return "";
    }
    return typeof v === "object" ? JSON.stringify(v) : String(v);
  });
}
function _looseEq(actual, want) {
  if (typeof actual === "number") return actual === parseFloat(want);
  if (typeof actual === "boolean") return String(actual) === String(want).trim();
  if (actual === null) return String(want).trim() === "null";
  return String(actual) === String(want);
}
function _assertOne(a, r) {
  var actual;
  if (a.source === "status") actual = r.status;
  else if (a.source === "body") actual = r.body;
  else {
    var p;
    try { p = JSON.parse(r.body); } catch (e) { return false; }
    actual = _get(p, a.path);
  }
  if (a.op === "exists") return actual !== undefined && actual !== null;
  if (actual === undefined) return false;
  var got = typeof actual === "object" ? JSON.stringify(actual) : String(actual);
  switch (a.op) {
    case "contains": return got.indexOf(a.value) >= 0;
    case "eq": return _looseEq(actual, a.value);
    case "ne": return !_looseEq(actual, a.value);
    case "gt": return parseFloat(actual) > parseFloat(a.value);
    case "lt": return parseFloat(actual) < parseFloat(a.value);
    case "gte": return parseFloat(actual) >= parseFloat(a.value);
    case "lte": return parseFloat(actual) <= parseFloat(a.value);
  }
  return false;
}
function _runStep(step, vars, idx) {
  var headers = {};
  if (step.headers) for (var k in step.headers) headers[k] = _interp(step.headers[k], vars);
  var url = _interp(step.url, vars);
  if (step.params && step.params.length) {
    // Interpolate each value, THEN URL-encode, so {{var}} resolves before
    // escaping (encoding first would turn "{{" into "%7B%7B" and break it).
    var _qs = [];
    for (var _pi = 0; _pi < step.params.length; _pi++) {
      var _p = step.params[_pi];
      if (!_p.key) continue;
      _qs.push(encodeURIComponent(_p.key) + "=" + encodeURIComponent(_interp(_p.value, vars)));
    }
    if (_qs.length) url += (url.indexOf("?") >= 0 ? "&" : "?") + _qs.join("&");
  }
  var body = step.body ? _interp(step.body, vars) : "";
  var r = http.request(step.method || "GET", url, body, { headers: headers });
  var label = step.name || ("step" + (idx + 1));
  var ok = true, reason = "";
  if (step.asserts && step.asserts.length) {
    for (var i = 0; i < step.asserts.length; i++) {
      var a = step.asserts[i];
      var pass = _assertOne(a, r);
      if (!pass && ok) { ok = false; reason = "assert " + a.source + " " + a.op + " " + (a.value || ""); }
    }
  } else {
    ok = r.status < 400 && !r.error;
    if (!ok) reason = r.error ? r.error : "status " + r.status;
  }
  // Extraction runs before emit so a broken chain surfaces as a step failure
  // instead of silently feeding empty {{vars}} into later steps: an extract is
  // configured but the body is missing/unparsable (extract_failed) or a path
  // resolves to nothing (extract_missing).
  if (step.extracts && step.extracts.length) {
    var parsed = null, parseOK = false;
    if (r.body) { try { parsed = JSON.parse(r.body); parseOK = true; } catch (e) {} }
    if (!parseOK && ok) { ok = false; reason = "extract_failed"; }
    for (var j = 0; j < step.extracts.length; j++) {
      var ex = step.extracts[j];
      var val = parseOK ? _get(parsed, ex.path) : undefined;
      vars[ex["var"]] = val;
      if (parseOK && val === undefined && ok) { ok = false; reason = "extract_missing"; }
    }
  }
  // Emit this step as its own labeled result (per-interface metrics + drill-down).
  __emit__({
    group: label, method: step.method || "GET", url: url,
    status: r.status, ok: ok, error_kind: ok ? "" : reason,
    latency_us: Math.round((r.duration_ms || 0) * 1000), ttfb_us: Math.round((r.duration_ms || 0) * 1000),
    sent_bytes: body.length, recv_bytes: (r.body || "").length,
    req_body: body, resp_body: r.body || ""
  });
  return { r: r, ok: ok, ms: r.duration_ms || 0 };
}
function iteration() {
  var vars = {};
  if (typeof nextRow === "function") {
    var row = nextRow();
    if (row) for (var k in row) vars[k] = row[k];
  }
  var steps = __SCENARIO__.steps;
  if (__SCENARIO__.mode === "weighted") {
    var total = 0;
    for (var i = 0; i < steps.length; i++) total += steps[i].weight || 1;
    var pick = Math.random() * total, acc = 0, chosen = 0;
    for (var i = 0; i < steps.length; i++) {
      acc += steps[i].weight || 1;
      if (pick <= acc) { chosen = i; break; }
    }
    _runStep(steps[chosen], vars, chosen);
  } else {
    // Sequence: run all steps, then emit a transaction total (end-to-end).
    var txnMs = 0, txnOK = true;
    for (var i = 0; i < steps.length; i++) {
      var out = _runStep(steps[i], vars, i);
      txnMs += out.ms;
      if (!out.ok) txnOK = false;
    }
    if (steps.length > 1) {
      __emit__({
        group: "txn:" + (__SCENARIO__.name || "scenario"),
        ok: txnOK, error_kind: txnOK ? "" : "step_failed",
        latency_us: Math.round(txnMs * 1000), ttfb_us: Math.round(txnMs * 1000)
      });
    }
  }
}
`
