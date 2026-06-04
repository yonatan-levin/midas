# Runbook — capturing a fresh replay/accuracy baseline

**Purpose:** produce a complete, replay-grade set of artifact bundles at the
current `CalculationVersion`, for both `cmd/replay --diff-stages` regression and
`cmd/accuracy` reporting. Supersedes the stale `CalculationVersion 4.1` baselines
under `artifacts/tier2-baseline/2026-05-15/` and `2026-05-19/`.

**Last run:** 2026-06-03 — `CalculationVersion 4.4`, 10-ticker basket, landed at
`artifacts/tier2-baseline/2026-06-03/` (referenced by `cmd/accuracy`).

---

## 1. Why a fresh baseline was needed

`docs/reviewer/DC-1-phase-5-replay-verification-followup.md` §3 records that the
only baseline available was `4.1`, which predates the assumption-profile config
**and** DC-1 phases 2–4. Any replay diff against it conflates the whole
`4.1 → 4.4` span and cannot attribute drift to a single phase. A baseline captured
at the current engine version removes that confound for all future replays.

## 2. Prerequisites & what works without them

| Dependency | Needed for | If absent |
|---|---|---|
| Live SEC EDGAR (`data.sec.gov`) | financial filings | **required** (was reachable) |
| Yahoo / Finzive | price, beta, shares | gateway retries + cookie/crumb; DB cache covers transient 429s |
| `FRED_API_KEY` | live treasury curve + FX | **optional** — `macro.fred_enabled` defaults `false`; the gateway falls back to a config-snapshot treasury curve (`internal/infra/gateways/macro/gateway.go`, "Using config-based treasury rates fallback") and `config/fx_rates.json`. The 2026-06-03 capture used this fallback; the curve was well-formed (10Y = 4.5%). |
| Seeded API key | auth | `go run ./cmd/seed-demo-key -db ./data/midas.db` prints one |

> **Determinism note.** The config-fallback macro path is actually *good* for a
> replay baseline (static inputs → reproducible), but it means the baseline's
> risk-free rate is the config snapshot, not live FRED. For a strict accuracy run
> you want a `FRED_API_KEY` so WACC uses the live curve.

## 3. Capture recipe (what was run 2026-06-03)

Dev mode (`ENVIRONMENT` unset) flips `logging.artifact_store.enabled = true` and
`triggers.manual = true`, so `?trace=1` writes a full bundle. **The first request
for each ticker must be the `?trace=1` one** — a warm valuation cache
(`valuation:v4:<ticker>`) short-circuits the pipeline and yields a *thin* bundle
(response only, no fetch/clean/value stages). Using a second server instance on a
spare port guarantees a cold cache.

```bash
# 1. Build + seed a key
go build -o /tmp/midas-server.exe ./cmd/server
go run ./cmd/seed-demo-key -db ./data/midas.db          # prints DEMO_API_KEY=dcf_...

# 2. Start a cold-cache instance on a spare port (dev defaults => capture on)
SERVER_PORT=8090 PORT=8090 SCHEDULER_ENABLED=false /tmp/midas-server.exe &

# 3. Capture each ticker once, cold (first request = full bundle)
KEY=dcf_...   # from step 1
for T in AAPL AMD EQIX F JPM KO MSFT MXL NVDA PLD; do
  curl -s -H "X-API-Key: $KEY" "http://localhost:8090/api/v1/fair-value/$T?trace=1" -o /dev/null
done

# 4. Promote the day's captures into the durable, git-tracked baseline subtree
#    (.gitignore tracks ONLY artifacts/tier2-baseline/**)
DST=artifacts/tier2-baseline/2026-06-03
for T in AAPL AMD EQIX F JPM KO MSFT MXL NVDA PLD; do
  mkdir -p "$DST/$T"
  cp -r "artifacts/$(date +%F)/$T/req_"* "$DST/$T/"
done
```

PowerShell equivalent for steps 2–3 (the Windows dev environment — see the `.ps1`
companions shipped alongside every `.sh` script in `scripts/`):

```powershell
# 2. Start a cold-cache instance on a spare port
$env:SERVER_PORT=8090; $env:PORT=8090; $env:SCHEDULER_ENABLED='false'
Start-Process -NoNewWindow $env:TEMP\midas-server.exe

# 3. Capture each ticker once, cold
$key = 'dcf_...'
foreach ($t in 'AAPL','AMD','EQIX','F','JPM','KO','MSFT','MXL','NVDA','PLD') {
  Invoke-WebRequest -Headers @{ 'X-API-Key' = $key } `
    "http://localhost:8090/api/v1/fair-value/$t`?trace=1" -OutFile $null | Out-Null
}
```

**Completeness check:** every bundle should carry `05-fetch-sec.raw.json`,
`06/07`, `10-clean-output.json`, `12/13/15`, `17-response.json`. The one expected
gap is **JPM 7/8** — DDM/bank tickers skip the cleaner snapshot (`10-clean-output`
absent), the known **RPL-8** issue; JPM replays need `--allow-schema-drift`.

## 4. Open operator residuals (DC-1 phase-5 §4)

The fresh baseline closes the "no current-version baseline" gap. Three
Phase-5-attributable confirmations still need infra not present in this session:

### §4.1 — strict 4.3→4.4 (Phase-5-only) isolation — needs `FRED_API_KEY`
Capture a `4.3` (pre-Phase-5-tip) baseline with live FRED, then replay the Phase-5
ship SHA against it. Expected: non-DDM zero numeric drift; DDM `EnterpriseValue`
zero-drift on non-B-rule banks; only the `calculation_version` string changes.

### §4.2 — B-rule-firing bank — confirms the DDM `+DebtLikeClaims` EV correction
JPM/BAC/WFC fire **zero** B-rules, so P5-C1's correction is a verified `+0` on the
basket. Add a bank that fires **B1** (operating leases), **B2** (pension), or
**B3** (litigation overlay) and confirm its `enterprise_value` rises by exactly the
B1+B2+B3 `DebtLikeClaims` sum.

Candidate tickers to probe (verify which actually fire B-rules by inspecting
`10-clean-output.json` / the ledger): **USB, PNC, TFC, KEY, RF** (regional banks
with material operating-lease + pension footprints) and **MET, PRU** (insurers
with large pension/contingent items, route via `FIN_INSURANCE`). Capture with
`?trace=1`, then grep the bundle:

```bash
go run ./cmd/replay --from=raw artifacts/<date>/<BANK>/req_*/   # sanity
# inspect ledger overlays for Field:"DebtLikeClaims" in 10-clean-output.json
```

### §4.3 — cleaning-rule-firing ticker — confirms the P5-C3-full projection
The basket fires ~zero cleaning adjustments (`cleaning_adjustments` absent/null).
Capture a ticker that fires A/C-rules (goodwill writedown, intangible/inventory
writedown, restructuring, capitalized interest) and confirm the projected
`cleaning_adjustments` rows (RuleID/Category/Type/Amount/Percentage/FromAccount/
ToAccount) match expectations. Candidates: recent large goodwill-impairment or
restructuring filers (e.g. **INTC**, **WBD**, **PARA**, **VFC**) — verify against
the firing rules in `internal/services/datacleaner/adjustments/`.

## 5. Close criteria

This runbook's §3 (fresh 4.4 baseline) is **done**. §4.1–§4.3 close when captured
and replayed per the Phase-5 spec §5 per-ticker expectation; until then they remain
operator follow-ups tracked in
`docs/reviewer/DC-1-phase-5-replay-verification-followup.md`.
