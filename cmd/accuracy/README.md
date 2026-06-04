# `cmd/accuracy` — valuation-accuracy harness

An **offline, read-only** harness that turns a directory of captured artifact
bundles into a ranked accuracy report: for each ticker it compares the engine's
intrinsic value (`dcf_value_per_share`) against the market price and surfaces the
systematic red flags an accuracy review needs to see at a glance.

It is hermetic by construction — it touches **no** database, network, or live
engine. It only reads the JSON files inside bundles that the server already wrote
under `./artifacts/...` (see "Capturing a baseline" below). This mirrors the
hermeticity contract of `cmd/replay`.

## Usage

```bash
# Markdown report (default) over the fresh 4.4 baseline
go run ./cmd/accuracy --dir artifacts/tier2-baseline/2026-06-03

# CSV for spreadsheets / dashboards
go run ./cmd/accuracy --dir artifacts/tier2-baseline/2026-06-03 --format csv

# Write to a file instead of stdout
go run ./cmd/accuracy --dir artifacts/tier2-baseline/2026-06-03 --out docs/accuracy/report-2026-06-03.md
```

| Flag | Default | Meaning |
|---|---|---|
| `--dir` | `artifacts/tier2-baseline/2026-06-03` | Baseline directory; one subdirectory per ticker, each holding one or more `req_*` bundle dirs. The latest bundle per ticker is analyzed. |
| `--format` | `md` | `md` (Markdown table + summary) or `csv`. |
| `--out` | _stdout_ | Write the report to this file. |

It reads two files per bundle: `17-response.json` (the served `FairValueResponse`)
and `14-model-selection.json` (the router's chosen model). The latter is optional —
absent simply disables `MODEL_DIVERGENCE` detection for that row.

> **Intrinsic source.** The harness treats `dcf_value_per_share` as the intrinsic
> value for every model — which is correct for the current engine because the
> served response carries the model-routed intrinsic value in that field (e.g. JPM's
> DDM/DCF result, MXL's revenue-multiple result). If a future response shape ever
> stores a non-DCF model's intrinsic value in a *different* field, that row would
> read `0` here and show a −100% gap; revisit this field mapping if that happens.

## Flag taxonomy

| Flag | Fires when | Why it matters |
|---|---|---|
| `NEG_INTRINSIC` | `dcf_value_per_share < 0` | A negative intrinsic value is a model breakdown, not conservatism. |
| `TERMINAL_DOMINANCE` | `dcf_terminal_pct_of_ev > 0.80` | The valuation rests almost entirely on the terminal multiple; the explicit FCF window contributes ~nothing (or is negative). |
| `NEG_FCF_YEARS` | any `dcf_per_year_pv` entry `< 0` | The multi-stage projection is producing negative free cash flow in the explicit window — the dominant driver of systematic undervaluation. |
| `MODEL_DIVERGENCE` | router-selected model ≠ computed model family | e.g. JPM: router selects `ddm` but the engine computes `dcf` (the T2-BS-1 `DividendsPerShare=0` fallback). |
| `EXTREME_GAP` | `|intrinsic / price − 1| > 50%` | The intrinsic value is wildly disconnected from market — worth a human look either way. |
| `SANITY_BLINDSPOT` | sanity-check says `is_reasonable` **and** the gap is extreme | The crosscheck is anchored to the model's own implied multiples, so it can rubber-stamp a systematically depressed value. |
| `CLASSIFIER_MISMATCH` | `industry.match == false` | SIC classifier disagrees with the balance-sheet heuristic (tracked separately by the classification-unification spec). |

## Interpreting the report

Rows are ranked by **descending absolute price gap** (biggest mispricing first).
The summary reports the mean absolute gap, how many tickers value below market, and
a flag rollup. A healthy engine on a diversified basket should show a *low* mean
gap with flags concentrated on genuinely hard-to-DCF names — not, as the
2026-06-03 baseline shows, `EXTREME_GAP` on 10/10 and negative intrinsic on
defensive cash-cows like KO. That report is the evidence base for a follow-up
`/debug` investigation into the FCF projection (tracked separately — **not** in
scope for this harness, which only *measures*).

## Capturing a baseline

The harness consumes bundles; it does not produce them. To refresh the baseline,
run the server in dev mode and drive `?trace=1` requests against a cold cache —
see [`docs/accuracy/baseline-capture-runbook.md`](../../docs/accuracy/baseline-capture-runbook.md).

## Tests

```bash
go test ./cmd/accuracy
```

`main_test.go` is table-driven and pins the metric math and every flag against the
real signatures observed in the 4.4 baseline (NVDA terminal-dominance, JPM
model-divergence, KO negative-intrinsic, plus a healthy control).
