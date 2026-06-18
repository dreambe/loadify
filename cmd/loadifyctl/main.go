// Command loadifyctl drives the loadify apisrv from the shell — for humans, CI,
// and agents. It exposes subcommands with machine-readable --json output and
// stable exit codes so an autonomous caller can script the full test lifecycle.
//
// Exit codes: 0 ok · 1 error · 2 usage · 3 SLA breach (run failed thresholds).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dreambe/loadify/internal/apiclient"
)

const usage = `loadifyctl — drive the loadify load-testing API

Usage: loadifyctl [--api URL] [--token T | --email E --password P] <command> [args]

Commands:
  run        --url U [--method M | --script F] [--vus N | --rps N] [--duration D] [--workers N] [--name S]
             Create a test and run it to completion (smoke test); prints the run.
  tests list                       List test definitions
  tests get <id>                   Show one test definition
  tests import <format> <file>     Convert curl|har|postman|openapi to a draft (format=- reads stdin)
  runs list                        List recent runs
  run status <id>                  Show a run's status + summary
  run stop <id>                    Stop a running run
  workers                          List connected workers

Global flags (before the command):
  --api URL        apisrv base URL (default http://localhost:8080, env LOADIFY_API)
  --token T        bearer token (env LOADIFY_TOKEN)
  --email/-password  login if no token (env LOADIFY_EMAIL / LOADIFY_PASSWORD)
  --json           machine-readable JSON output
`

func main() {
	var (
		api      = flag.String("api", env("LOADIFY_API", "http://localhost:8080"), "apisrv base URL")
		token    = flag.String("token", os.Getenv("LOADIFY_TOKEN"), "bearer token")
		email    = flag.String("email", os.Getenv("LOADIFY_EMAIL"), "login email")
		password = flag.String("password", os.Getenv("LOADIFY_PASSWORD"), "login password")
		jsonOut  = flag.Bool("json", false, "machine-readable JSON output")
		// run-subcommand flags (parsed from the global set for backward-compat).
		url      = flag.String("url", "", "target URL (run)")
		method   = flag.String("method", "GET", "HTTP method (run)")
		script   = flag.String("script", "", "goja script path (run; implies protocol=script)")
		vus      = flag.Int("vus", 20, "virtual users (run)")
		rps      = flag.Int("rps", 0, "target req/s; open model (run)")
		dur      = flag.Duration("duration", 15*time.Second, "duration (run)")
		workers  = flag.Int("workers", 0, "desired workers (run)")
		name     = flag.String("name", "loadifyctl-run", "test name (run)")
	)
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}

	c := apiclient.New(*api, *token)
	ctx := context.Background()
	if c.Token == "" && *email != "" {
		if _, err := c.Login(ctx, *email, *password); err != nil {
			fail(err)
		}
	}

	out := &printer{json: *jsonOut}
	switch args[0] {
	case "run":
		// `run status|stop <id>` vs the smoke-test `run`.
		if len(args) >= 2 && (args[1] == "status" || args[1] == "stop") {
			runSub(ctx, c, out, args[1:])
			return
		}
		cmdRun(ctx, c, out, runOpts{
			url: *url, method: *method, script: *script, vus: *vus,
			rps: *rps, dur: *dur, workers: *workers, name: *name,
		})
	case "runs":
		if len(args) >= 2 && args[1] == "list" {
			runs, err := c.ListRuns(ctx)
			must(err)
			out.runs(runs)
			return
		}
		usageErr("runs list")
	case "tests":
		cmdTests(ctx, c, out, args[1:])
	case "workers":
		ws, err := c.ListWorkers(ctx)
		must(err)
		out.workers(ws)
	case "help", "-h", "--help":
		flag.Usage()
	default:
		usageErr("unknown command " + args[0])
	}
}

type runOpts struct {
	url, method, script, name string
	vus, rps, workers         int
	dur                       time.Duration
}

func cmdRun(ctx context.Context, c *apiclient.Client, out *printer, o runOpts) {
	if o.dur <= 0 {
		usageErr("--duration must be > 0")
	}
	if o.rps < 0 {
		usageErr("--rps must be >= 0")
	}
	if o.workers < 0 {
		usageErr("--workers must be >= 0")
	}
	if o.rps == 0 && o.vus <= 0 {
		usageErr("--vus must be > 0")
	}
	proto := "http"
	var scriptJS string
	if o.script != "" {
		proto = "script"
		b, err := readFileCapped(o.script)
		must(err)
		scriptJS = string(b)
	}
	plan := map[string]any{"protocol": proto}
	if proto == "http" {
		if o.url == "" {
			usageErr("--url is required")
		}
		if strings.HasPrefix(strings.ToLower(o.url), "https") {
			plan["protocol"] = "https"
			proto = "https"
		}
		plan["http"] = map[string]any{"method": o.method, "url": o.url}
	}
	var ramp []map[string]any
	if o.rps > 0 {
		ramp = []map[string]any{{"duration_ms": o.dur.Milliseconds(), "target_rps": o.rps}}
	} else {
		ramp = []map[string]any{{"duration_ms": o.dur.Milliseconds(), "target_vus": o.vus}}
	}
	testID, err := c.CreateTest(ctx, apiclient.CreateTestRequest{
		Name: o.name, Protocol: proto, Plan: plan, Ramp: ramp, Script: scriptJS,
	})
	must(err)
	runID, err := c.StartRun(ctx, testID, o.workers)
	must(err)
	// Allow generous headroom over the nominal duration: queueing, ramp tails and
	// drain can push a run well past o.dur. Ctrl+C still exits early.
	wctx, cancel := context.WithTimeout(ctx, o.dur*2+5*time.Minute)
	defer cancel()
	run, err := c.WaitForRun(wctx, runID, 2*time.Second)
	must(err)
	out.run(run)
	if run.Status == "failed" {
		os.Exit(3)
	}
	if run.Status == "aborted" {
		os.Exit(1)
	}
}

func runSub(ctx context.Context, c *apiclient.Client, out *printer, args []string) {
	if len(args) < 2 {
		usageErr("run " + args[0] + " <id>")
	}
	switch args[0] {
	case "status":
		run, err := c.GetRun(ctx, args[1])
		must(err)
		out.run(run)
	case "stop":
		must(c.StopRun(ctx, args[1]))
		out.ok("stopping " + args[1])
	}
}

func cmdTests(ctx context.Context, c *apiclient.Client, out *printer, args []string) {
	if len(args) == 0 {
		usageErr("tests list|get|import")
	}
	switch args[0] {
	case "list":
		ts, err := c.ListTests(ctx)
		must(err)
		out.tests(ts)
	case "get":
		if len(args) < 2 {
			usageErr("tests get <id>")
		}
		td, err := c.GetTest(ctx, args[1])
		must(err)
		out.any(td)
	case "import":
		if len(args) < 3 {
			usageErr("tests import <format> <file|->")
		}
		content := readFileOrStdin(args[2])
		draft, err := c.ImportTest(ctx, args[1], content)
		must(err)
		out.any(draft)
	default:
		usageErr("tests list|get|import")
	}
}

// --- output ---

type printer struct{ json bool }

func (p *printer) any(v any) { b, _ := json.MarshalIndent(v, "", "  "); fmt.Println(string(b)) }
func (p *printer) ok(msg string) {
	if p.json {
		p.any(map[string]string{"status": "ok", "message": msg})
		return
	}
	fmt.Println(msg)
}
func (p *printer) run(r *apiclient.Run) {
	if p.json {
		p.any(r)
		return
	}
	fmt.Printf("run %s  %s  %s\n", r.ID, r.Status, r.Name)
	if len(r.Summary) > 0 {
		fmt.Println(string(r.Summary))
	}
}
func (p *printer) runs(rs []apiclient.Run) {
	if p.json {
		p.any(rs)
		return
	}
	for _, r := range rs {
		fmt.Printf("%s  %-10s  %s\n", r.ID, r.Status, r.Name)
	}
}
func (p *printer) tests(ts []apiclient.TestSummary) {
	if p.json {
		p.any(ts)
		return
	}
	for _, t := range ts {
		fmt.Printf("%s  %-10s  %s\n", t.ID, t.Protocol, t.Name)
	}
}
func (p *printer) workers(ws []apiclient.Worker) {
	if p.json {
		p.any(ws)
		return
	}
	for _, w := range ws {
		fmt.Printf("%s  %-8s  region=%s  vus=%d\n", w.WorkerID, w.Status, w.Region, w.ActiveVUs)
	}
}

// --- helpers ---

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// maxInputBytes caps script and import inputs so a pathological file can't be
// slurped whole into memory and shipped to the API.
const maxInputBytes = 10 << 20 // 10 MiB

func readFileOrStdin(path string) string {
	var r io.Reader = os.Stdin
	if path != "-" {
		f, err := os.Open(path)
		must(err)
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, maxInputBytes+1))
	must(err)
	if len(b) > maxInputBytes {
		fail(fmt.Errorf("input exceeds %d byte limit", maxInputBytes))
	}
	return string(b)
}

func readFileCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxInputBytes {
		return nil, fmt.Errorf("%s exceeds %d byte limit", path, maxInputBytes)
	}
	return b, nil
}

func must(err error) {
	if err != nil {
		fail(err)
	}
}
func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
func usageErr(msg string) {
	fmt.Fprintln(os.Stderr, "usage:", msg)
	os.Exit(2)
}
