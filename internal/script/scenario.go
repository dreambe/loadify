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
function _interp(s, vars) {
  if (s === undefined || s === null) return s;
  return String(s).replace(/\{\{\s*([\w.]+)\s*\}\}/g, function (m, k) {
    var v = vars[k];
    return v === undefined || v === null ? "" : (typeof v === "object" ? JSON.stringify(v) : String(v));
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
function _runStep(step, vars) {
  var headers = {};
  if (step.headers) for (var k in step.headers) headers[k] = _interp(step.headers[k], vars);
  var url = _interp(step.url, vars);
  var body = step.body ? _interp(step.body, vars) : "";
  var r = http.request(step.method || "GET", url, body, { headers: headers });
  var label = step.name || ((step.method || "GET") + " " + url);
  if (step.asserts && step.asserts.length) {
    for (var i = 0; i < step.asserts.length; i++) {
      var a = step.asserts[i];
      check(label + " · " + a.source + " " + a.op + " " + (a.value || ""), _assertOne(a, r));
    }
  } else {
    check(label, r.status < 400);
  }
  if (step.extracts && step.extracts.length && r.body) {
    var parsed = null;
    try { parsed = JSON.parse(r.body); } catch (e) {}
    for (var j = 0; j < step.extracts.length; j++) {
      var ex = step.extracts[j];
      vars[ex["var"]] = parsed !== null ? _get(parsed, ex.path) : undefined;
    }
  }
  return r;
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
    var pick = Math.random() * total, acc = 0, chosen = steps[0];
    for (var i = 0; i < steps.length; i++) {
      acc += steps[i].weight || 1;
      if (pick <= acc) { chosen = steps[i]; break; }
    }
    _runStep(chosen, vars);
  } else {
    for (var i = 0; i < steps.length; i++) _runStep(steps[i], vars);
  }
}
`
