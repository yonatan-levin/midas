# SEC Data Cleaning Field Guide

Below is a field‑guide style checklist you can wire straight into your data‑cleaning pipeline.  
Items are grouped where “accounting tricks” typically hide:  
**A – Over‑stated / low‑quality assets**  
**B – Under‑stated liabilities & commitments**  
**C – Distorted earnings / cash‑flow lines**

| # | Item to Scrutinise (10‑K / 10‑Q tag or note) | Why it distorts reality | Adjustment playbook |
|---|---|---|---|
| **A Over‑stated / low‑quality assets** | | | |
| A1 | Goodwill (`GoodwillGross`, `GoodwillNet`) | Pure acquisition premium, no guarantee of future cash flow | Exclude from invested capital; track impairments separately |
| A2 | Indefinite‑lived intangibles (trademarks, broadcast licences) | Carrying value can be inflated through optimistic tests | Write down to zero for asset‑backing ratios; amortise over a conservative life (≈10 yrs) in DCF if retained |
| A3 | Capitalised software / R&D (`SoftwareDevelopmentCostsCapitalized`) | Capitalising postpones expenses, inflating EBIT | Reclassify to operating expense spread over development cycle unless strong evidence it’s a revenue‑generating asset |
| A4 | Deferred tax assets (`DeferredTaxAssetsGross`) | DTAs only valuable if future taxable income exists | Reduce by the portion not “more‑likely‑than‑not” to be realised (check valuation‑allowance note) |
| A5 | Slow / obsolete inventory (`InventoryNet`) | May sit at cost above NRV | Force write‑down to net realisable value or remove from liquidation‑value models |
| A6 | Right‑of‑use assets (`OperatingLeaseRightOfUseAsset`) | ROU assets inflate total assets but don’t add operating capacity | Treat ROU asset as offset to lease liability; exclude from invested capital when benchmarking ROIC |
| A7 | Excess cash / short‑term investments | Non‑operating; flatters liquidity ratios | Remove from working capital; subtract from enterprise value when running EV multipliers |
| **B Under‑stated liabilities & off‑balance‑sheet exposures** | | | |
| B1 | Operating lease liability (`OperatingLeaseLiabilityCurrent`,`OperatingLeaseLiabilityNoncurrent`) | Contractual debt often ignored pre‑ASC 842 | Treat as debt for leverage and EV; discount remaining payments at incremental borrowing rate |
| B2 | Under‑funded pensions / OPEB (`PensionLiabilities`,`OPEBLiability`) | Economic debt hidden in footnotes | Add net under‑funded amount to debt; adjust EV and credit metrics |
| B3 | Contingent & environmental liabilities | Often disclosed only qualitatively | Use probability‑weighted expected value; sensitivity‑test higher scenarios |
| **C Earnings / cash‑flow distortion items** | | | |
| C1 | Restructuring / integration charges (`RestructuringCosts`) | “Big‑bath” charges create future expense slush funds | Strip from EBITDA/EBIT; capitalise cash component and amortise if recurring |
| C2 | Asset‑sale gains / impairment losses | Non‑core; volatile | Remove from operating profit; place in non‑operating section |
| C3 | Litigation settlements & fines | Episodic but material | Exclude from core earnings; disclose separately in quality‑of‑earnings notes |
| C4 | Stock‑based compensation | Non‑cash yet dilutive; GAAP adds back to CFO | Deduct SBC from FCF; include dilution in per‑share estimates |
| C5 | Fair‑value gains / losses on derivatives & investments | Volatile marks obscure operating trend | Segregate into financial income / expense; normalise earnings |
| C6 | Capitalised interest | Boosts EBIT by burying financing cost in PP&E | Add back to interest expense; reduce PP&E accordingly |
| C7 | Quarter‑end working‑capital “window dressing” | Temporary inflows/outflows inflate liquidity | Use average‑quarter WC; scrutinise unusual receivable / payable spikes |

---

## How to operationalise this in code (“cleaning rules”)

1. **Tag mapping** – Load the SEC XBRL taxonomy; map each line above to the full set of equivalent tags (e.g., `GoodwillGross`, `GoodwillNet`, `GoodwillAmortization`).  
2. **Rule engine** – Build a rules table (JSON/YAML) where each tag has fields like:  

   ```json
   {
     "tag": "GoodwillNet",
     "adjustment": "delete_from_assets",
     "note": "exclude from invested_capital"
   }
   ```

3. **Automated flags**  
   * Goodwill + other intangibles > 40 % of total assets → flag “possible impairment risk”.  
   * Valuation‑allowance < full DTA × (1 – 5‑yr avg cash tax rate) → flag.  

4. **Re‑statement layer** – Apply the rules to generate an *adjusted* income statement, balance sheet, and cash‑flow. Store original **and** adjusted versions for an audit trail.  
5. **Unit tests (TDD)** – For each rule, create fixtures from known filings (e.g., UPS 2020 for goodwill, a retailer with obsolete inventory). Assert that the rule removes / reclassifies the target line and leaves others untouched.  
6. **Version control** – Ship rules as a versioned package; breaking changes trigger a full test‑suite and diff report.

---

## No illusions: limits & next steps

* **Judgement calls remain.** Some tricks (e.g., capitalised R&D) need industry context; surface them for analyst review rather than hard‑delete.  
* **Footnote mining required.** Contingent liabilities and hidden pension details live in narrative notes – natural‑language extraction is mandatory.  
* **Global GAAP drift.** IFRS entities use different tags (IAS 38 intangibles, IAS 19 pensions). Keep a mapping table and flag cross‑filers.

---

*Prepared June 27 2025*  
