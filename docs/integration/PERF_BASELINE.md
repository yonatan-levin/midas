# Performance Baseline (Phase 2.5)

Purpose: Establish initial latency/throughput baseline for `/api/v1/fair-value/{ticker}` under light load on a developer machine to guide future tuning.

How to run locally
- Apply schema & migrations: `go run ./cmd/migrate -db ./data/midas.db`
- Start the API server (staging or local). Windows quick path: `./scripts/contract_fuzz.ps1 -DemoKey '<API_KEY>' -InstallSchemathesis`
- Run the built-in load tester for 60s at 20 RPS and 20 concurrency:

```
go run ./scripts/load_tester.go -url http://localhost:8080 -key <API_KEY> -type single -concurrency 20 -duration 60s -rps 20 -output performance/results/baseline_20rps_60s.json
```

Acceptance target (MVP)
- p95 latency: < 300 ms
- Error rate: < 1%
- Throughput: >= 20 RPS sustained on dev hardware

Notes
- Local runs include auth + metrics + rate-limit middleware
- External I/O minimized in tests via seeded data and memory cache fallback
- For real external dependencies, enable live tests (E2E_LIVE=1) and re-run baseline accordingly
- Enable pprof by setting `ENABLE_PPROF=true` and access `/debug/pprof/` for profiling.


