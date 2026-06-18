package httpd

import (
	"strings"
	"testing"

	"github.com/dreambe/loadify/internal/plan"
)

func TestEvalAsserts(t *testing.T) {
	body := []byte(`{"code":0,"ok":true,"data":{"user":{"id":42,"name":"shark"},"items":[{"sku":"a-1"},{"sku":"b-2"}]},"msg":null}`)

	cases := []struct {
		name    string
		a       plan.HTTPAssert
		wantErr string // "" = pass; otherwise substring of the failure reason
	}{
		{"status eq pass", plan.HTTPAssert{Source: "status", Op: "eq", Value: "200"}, ""},
		{"status eq fail", plan.HTTPAssert{Source: "status", Op: "eq", Value: "201"}, "got 200"},
		{"status lt pass", plan.HTTPAssert{Source: "status", Op: "lt", Value: "400"}, ""},
		{"body contains pass", plan.HTTPAssert{Source: "body", Op: "contains", Value: "shark"}, ""},
		{"body contains fail", plan.HTTPAssert{Source: "body", Op: "contains", Value: "whale"}, "assert body"},
		{"json number eq", plan.HTTPAssert{Source: "json", Path: "code", Op: "eq", Value: "0"}, ""},
		{"json number ne fail", plan.HTTPAssert{Source: "json", Path: "code", Op: "ne", Value: "0"}, "got 0"},
		{"json bool true", plan.HTTPAssert{Source: "json", Path: "ok", Op: "eq", Value: "true"}, ""},
		{"json bool mismatch", plan.HTTPAssert{Source: "json", Path: "ok", Op: "eq", Value: "false"}, "got true"},
		{"json nested string", plan.HTTPAssert{Source: "json", Path: "data.user.name", Op: "eq", Value: "shark"}, ""},
		{"json nested number gt", plan.HTTPAssert{Source: "json", Path: "data.user.id", Op: "gt", Value: "40"}, ""},
		{"json nested number gt fail", plan.HTTPAssert{Source: "json", Path: "data.user.id", Op: "gt", Value: "50"}, "got 42"},
		{"json array index", plan.HTTPAssert{Source: "json", Path: "data.items.1.sku", Op: "contains", Value: "b-"}, ""},
		{"json exists pass", plan.HTTPAssert{Source: "json", Path: "data.user", Op: "exists"}, ""},
		{"json exists fail", plan.HTTPAssert{Source: "json", Path: "data.ghost", Op: "exists"}, "field missing"},
		{"json missing field eq", plan.HTTPAssert{Source: "json", Path: "data.user.phone", Op: "eq", Value: "1"}, "field missing"},
		{"json null eq null", plan.HTTPAssert{Source: "json", Path: "msg", Op: "eq", Value: "null"}, ""},
		{"json gt on string", plan.HTTPAssert{Source: "json", Path: "data.user.name", Op: "gt", Value: "5"}, "not a number"},
		{"json array out of range", plan.HTTPAssert{Source: "json", Path: "data.items.9.sku", Op: "exists"}, "field missing"},
	}
	for _, tc := range cases {
		got := evalAsserts([]plan.HTTPAssert{tc.a}, 200, body)
		if tc.wantErr == "" && got != "" {
			t.Errorf("%s: want pass, got %q", tc.name, got)
		}
		if tc.wantErr != "" && !strings.Contains(got, tc.wantErr) {
			t.Errorf("%s: want failure containing %q, got %q", tc.name, tc.wantErr, got)
		}
	}
}

func TestEvalAssertsBadJSON(t *testing.T) {
	got := evalAsserts(
		[]plan.HTTPAssert{{Source: "json", Path: "a.b", Op: "eq", Value: "1"}},
		200, []byte("<html>not json</html>"))
	if !strings.Contains(got, "not valid JSON") {
		t.Errorf("bad json: got %q", got)
	}
	// Status asserts still work on a non-JSON body.
	if got := evalAsserts([]plan.HTTPAssert{{Source: "status", Op: "eq", Value: "200"}}, 200, []byte("x")); got != "" {
		t.Errorf("status on non-json body: got %q", got)
	}
}
