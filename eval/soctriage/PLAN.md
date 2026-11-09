# SOC triage eval: plan

**Goal:** show Jev one real security alert at a time and see whether it can tell a real attack from noise, the call a junior SOC analyst makes all day.

**Data:** [SecAlertBench](https://github.com/Dxsssu/SecAlertBench). It has 8,322 alerts from the security teams of three large companies, each labeled Attack or Non-Attack, and each alert includes the web request that triggered it. No license is stated, so the data is downloaded locally and never committed.

## 1. Build the 4,000-alert set (English only): done

Run `task eval:soctriage:build`. It writes `eval/data/soctriage/alerts.jsonl`:

| Step | Alerts |
|---|---|
| Raw file (pinned commit, SHA-256 checked) | 8,322 |
| After removing duplicates and 16 keys with opposite labels | 5,811 |
| After dropping alerts whose traffic isn't English (Chinese pages, plus one Russian reply) | 4,076 |
| **Eval set** | **4,000**: 1,282 risky, 2,718 false_positive |

- The detector's Chinese labels are translated through `translations.json`: 215 rule names, 22 alert types and 14 kill-chain stages, checked by hand. CVE IDs in every rule name were matched against the originals. A missing translation fails the build; nothing Chinese passes through.
- Every risky alert is kept. The 76 false positives cut to reach 4,000 come from the rules with the most rows.
- Binary attack payloads (TLS handshakes, serialized Java objects) stay in. They contain stray non-ASCII bytes, but they are evidence, not a foreign language.
- SQL-injection false positives still make up nearly half the set (1,922 of the 2,718 false positives). That's what the source contains, and the rule-name baseline exists to expose it.

## 2. Ask the question

- One question per alert, with two answers:
  - **Risky** (`risky`): a real attack that an analyst should look at
  - **False Positive** (`false_positive`): the alert fired on harmless traffic and can be closed
- The dataset's Attack label maps to `risky` and Non-Attack to `false_positive`. Each result row stores the answer as `verdict`, and the report calls it the Verdict.
- Jev sees the alert as recorded: rule name, alert type, kill-chain stage, and the request and response. The label is left out, and so are the source and destination IPs and source port, which SecAlertBench replaced with random values.
- The instructions describe what each answer means; they don't hint which one is right. The answer definitions are in `criteria.json`.
- Two ways to ask, as in the tax eval: one choice between the two answers (default), or one yes/no "should this be escalated?" question that escalates at 0.5 or above (`-protocol noul`).
- Alerts go in batches of 500, or fewer if `TYPESAFE_MAX_ITEMS` is lower.

## 3. Score it

- **Attacks escalated**: the share of real attacks escalated. This is the main number, because a missed attack is the costly mistake.
- **False positives closed** and **false positives escalated**: how many harmless alerts it correctly closes, and how many it escalates anyway, which is what wears analysts out.
- The raw counts as a 2x2 table: attacks escalated or missed, false positives escalated or closed.
- Overall accuracy and F1.
- A **cheat baseline**: guess each alert's label from the other alerts under the same rule name. If a model can't beat that, it isn't reading the alert. It's strong: 92.8% accuracy, 88.3% of attacks escalated, 5.1% of false positives escalated. It's also a ceiling a zero-shot model can't really reach, since it learns from the eval set's own labels.
- How right it is when it sounds confident, plus cost per 100 alerts.

## 4. Code

Done. `eval/soctriage/` has `fetch`, `build`, `run` and `score`, with tests, built the same way as `eval/taxcat/`. Taskfile commands:

- `task eval:soctriage:build`: download and build the set
- `task eval:soctriage PROFILE=jev-1.14` (add `PROTOCOL=noul`, `LIMIT=10`, `NAME=jev-1.14.p1` as needed)
- `task eval:soctriage:score`: Markdown report of every saved run, beside the baseline

Runs land in gitignored `eval/results/soctriage/`. A run made on a different version of the set or the instructions is skipped when scoring.

## 5. Rollout

1. Try 10 alerts with Jev to make sure everything connects. **Done**: both protocols ran with no errors, at about $0.006 per 100 alerts, so a full pass costs about $0.24. Jev escalated all 3 attacks, but also 5 of the 7 false positives, all of them SQL-injection alerts on normal app traffic.
2. Do one full run, read the mistakes, and fix any unclear instructions. Then freeze them.
3. Run every model three times and publish the scores.

## First full run: jev-1.14, choice protocol (2026-10-26)

| | Attacks escalated | False positives escalated | Accuracy | Cost / 100 |
|---|---|---|---|---|
| Rule-name baseline | 88.3% | 5.1% | 92.8% | — |
| jev-1.14 at 0.5 (its own answer) | 100.0% | 67.2% | 54.4% | $0.0063 |
| jev-1.14, escalating only at p ≥ 0.9 | 97.0% | 13.4% | 89.9% | |
| jev-1.14, escalating only at p ≥ 0.95 | 94.4% | 8.1% | 92.7% | |

- Jev ranks alerts well (AUC 0.967) but leans hard toward escalate. Its answers are only as good as the cutoff.
- Where the 1,826 escalated false positives come from:
  - 1,375 are SQL-injection alerts on internal business apps that send SQL in their requests by design, e.g. `sqlstr=select count(1) from qo_lines…`, `initCond=1=1 and (…)`, and serialized Java RPC carrying queries. Several contain real injection-looking syntax such as `or 1=1`. Only knowing the app tells you they're normal.
  - 77 carry real exploit payloads (Log4j, Jenkins) calling back to scanner addresses. One callback address appears in 133 escalate alerts and 22 false_positive ones, so this is label noise or sanctioned scanning that the instructions can't fix.
  - The rest are smaller groups: empty-password logins by named users (54), directory traversal on normal client uploads (54), Java serialized RPC (47), script downloads (44), and SVN downloads of database scripts (34).
- Verdict on the instructions: most escalated false positives come from missing context, not unclear wording. Two answer definitions are loose enough to push toward escalate: "credential guessing" covers a single empty-password login, and "probing for sensitive files" covers normal downloads of files that sound sensitive.

## Second full run: tightened criteria (2026-10-26)

Changed only the two loose definitions, in `criteria.json` and the noul question. Login attacks are now "automated or default-credential login attempts", and a user mistyping or leaving out their own password is called out as a false positive. Sensitive files are now "files and paths a normal client has no reason to reach", and a client downloading files that are part of its normal work is called out as a false positive. Nothing about apps that send SQL. The first run is kept in `eval/results/soctriage/criteria-v1/`.

| | Attacks escalated | False positives escalated | Accuracy | AUC |
|---|---|---|---|---|
| Rule-name baseline | 88.3% | 5.1% | 92.8% | 0.982 |
| jev-1.14, first criteria | 100.0% | 67.2% | 54.4% | 0.967 |
| **jev-1.14, tightened criteria** | **99.8%** | **64.1%** | **56.4%** | **0.965** |

- The targeted groups moved as intended:
  - empty-password logins escalated: 100% → 4% (54 alerts)
  - administrator weak-password false positives: 100% → 22%
  - web weak-password false positives: 73% → 50%
  - The attack alerts in the same rules stayed at 100%.
- SVN downloads of database scripts didn't move (still 100% escalated). A URL full of `DBScript` with `DROP TABLE` in the reply still reads as sensitive.
- Cost: 3 borderline attacks now fall just under 0.5 (p 0.46–0.49). Two are probes for `/manifest.json` and `/lan/zh.json`; the third is an OPTIONS probe.
- 128 alerts were fixed and 43 newly broken. The newly escalated false positives are mostly SQL-injection alerts that moved slightly (about +0.09). SQL-rule alerts shifted 0.035 on average with 75 flips each way, which is roughly the noise from any change to the instructions.
- AUC didn't change. The wording fixes a few groups but not Jev's lean toward escalate, which is what separates its 56% accuracy from the ~92% it reaches if it escalates only at p ≥ 0.95.
- These criteria are now frozen for model comparisons.

## All profiles, one pass each (2026-10-27)

After renaming the answer to a Verdict (Risky or False Positive), every profile passed a 3-alert smoke test, then ran one full pass. Results page: https://claude.ai/artifact/RQcDN7wAP389gwk5pSBExX

| Setup | Accuracy | Attacks caught | False positives closed | AUC |
|---|---|---|---|---|
| Rule-name baseline | 92.8% | 88.3% | 94.9% | 0.982 |
| pplx-decider v1.2 | 89.2% | 99.9% | 84.2% | 0.972 |
| Mercury Decide | 86.8% | 98.8% | 81.2% | 0.973 |
| d1 Ultra | 66.4% | 99.8% | 50.6% | 0.972 |
| d1.1 | 57.0% | 100.0% | 36.6% | 0.959 |
| Jev 1.14 | 56.4% | 99.8% | 36.0% | 0.963 |
| GPT-6 Luna Decisions | 54.5% | 99.7% | 33.2% | 0.952 |
| d1 | 45.8% | 100.0% | 20.3% | 0.899 |
| Clef | 44.0% | 100.0% | 17.5% | 0.951 |
| pplx Flash v1.2 (M5 Max) | 40.7% | 99.1% | 13.2% | 0.693 |
| Clef Flash | 40.6% | 100.0% | 12.7% | 0.942 |
| kev-4b | 40.3% | 99.9% | 12.1% | 0.858 |
| pplx Flash v1.2 | 40.0% | 99.5% | 12.0% | 0.697 |
| Solar Decide | 35.1% | 100.0% | 4.5% | 0.457 |

d1 Ultra had 6 alerts with no answer and Mercury Decide 5; they count as wrong.

## Third verdict: Inconclusive, one pass each (2026-10-27)

`-protocol inconclusive` asks the same question with a third option: Inconclusive, "the alert does not show whether the traffic is an attack or harmless … telling them apart needs context the alert does not include." Inconclusive counts as wrong in accuracy, is kept off the misses list, and is reported on its own. `criteria.json` and the two-way questions are unchanged, so earlier runs still score. Every profile except pplx Flash on the Mac ran.

| Setup | Accuracy (two-way → three-way) | Attacks caught | False positives closed | False positives rated Risky | Inconclusive | Errors |
|---|---|---|---|---|---|---|
| pplx-decider v1.2 | 89.2% → 86.5% | 99.8% | 80.2% | 16.1% | 2.6% | 0 |
| Mercury Decide | 86.8% → 81.5% | 94.5% | 75.4% | 16.9% | 5.8% | 2 |
| d1 Ultra | 66.4% → **76.2%** | 99.7% | 65.2% | 34.5% | 0.1% | 5 |
| GPT-6 Luna Decisions | 54.5% → 55.5% | 97.0% | 35.9% | 57.2% | 4.9% | 16 |
| d1.1 (now on OpenRouter) | 57.0% → 54.2% | 99.9% | 32.6% | 66.9% | 0.3% | 0 |
| Jev 1.14 | 56.4% → 49.8% | 99.8% | 26.2% | 71.8% | 1.4% | 0 |
| d1 | 45.8% → 43.6% | 99.6% | 17.2% | 75.8% | 4.9% | 0 |
| pplx Flash v1.2 | 40.0% → 40.3% | 99.1% | 12.5% | 86.5% | 0.7% | 0 |
| kev-4b | 40.3% → 36.6% | 98.3% | 7.5% | 85.4% | 4.4% | 40 |
| Solar Decide | 35.1% → 34.6% | 99.9% | 3.8% | 91.6% | 3.2% | 0 |
| Clef | 44.0% → 33.7% | 99.8% | 2.5% | 58.3% | 26.7% | 0 |
| Clef Flash | 40.6% → 32.3% | 98.4% | 1.1% | 70.5% | 19.8% | 0 |

- Most models rarely pick Inconclusive (under 6% of alerts). The Clefs are the exception, at a fifth to a quarter.
- When they do, it's almost always on a false positive: Clef put 1,064 false positives and 2 attacks in Inconclusive. That's the right instinct, since most false positives are SQL from internal apps the alert can't vouch for. But the Clefs also stopped closing almost anything, so their accuracy fell.
- The extra option moves the two-way call too, not just the borderline cases. d1 Ultra closed more false positives (50.6% → 65.2%) and gained 10 points. Jev closed fewer (36.0% → 26.2%) and lost 7.
- Mercury Decide got worse on the costly mistake: 47 attacks rated False Positive, against 13 in two-way.
- Errors are upstream: Luna had 16 gateway timeouts, kev-4b 40 client timeouts, Mercury 2 bad-gateway errors and d1 Ultra 5 rate limits. They count as wrong.
- Conclusion: only d1 Ultra gets clearly more accurate with the third option (Luna and pplx Flash gain under a point), and no model uses Inconclusive as a precise "needs context" flag.

## Watch out for

- The README doesn't say who labeled the alerts. We trust the labels but spot-check 100 of them.
- "false_positive" includes what looks like sanctioned internal scanning. About half the Log4j alerts labeled false_positive carry real `${jndi:...}` payloads from one internal address. No single alert can show it was authorized, so some misses there are expected.
