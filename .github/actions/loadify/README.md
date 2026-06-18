# Loadify GitHub Action

Run a loadify load test from CI, summarize the result on the PR check, and
**fail the job on an SLA breach** (so a performance regression blocks merge).

It talks to a running loadify API server over HTTP (curl + jq, no binary to
install) — start a run by test id, wait for it to finish, write a results table
to the job summary, and exit non-zero if the run failed or breached its SLA
thresholds.

## Usage

```yaml
- uses: dreambe/loadify/.github/actions/loadify@main
  with:
    api-url: https://loadify.example.com
    token: ${{ secrets.LOADIFY_TOKEN }}   # an operator+ API token
    test-id: <your-test-definition-id>
    workers: 2                            # optional, default 1
    # name: "PR #${{ github.event.number }}"   # optional run name
    # timeout-seconds: 1800                     # optional, default 30m
```

Outputs: `run-id`, `passed` (`true`/`false`/`n/a`).

The PR check's **Summary** shows a table:

| Status | SLA | Total | p95 (ms) | Error % |
|---|---|---|---|---|
| completed | true | 21,591 | 105.9 | 0 |

plus a `p95 vs baseline` line when the test has a baseline run set — so a
regression vs the last good run is visible at a glance.
