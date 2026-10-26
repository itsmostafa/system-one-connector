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
| **Eval set** | **4,000**: 1,282 escalate, 2,718 false_positive |

- The detector's Chinese labels are translated through `translations.json`: 215 rule names, 22 alert types and 14 kill-chain stages, checked by hand. CVE IDs in every rule name were matched against the originals. A missing translation fails the build; nothing Chinese passes through.
- Every escalate alert is kept. The 76 false positives cut to reach 4,000 come from the rules with the most rows.
- Binary attack payloads (TLS handshakes, serialized Java objects) stay in. They contain stray non-ASCII bytes, but they are evidence, not a foreign language.
- SQL-injection false alarms still make up nearly half the set (1,922 of the 2,718 false positives). That's what the source contains, and the rule-name baseline exists to expose it.

## 2. Ask the question

- One question per alert, with two answers:
  - **escalate**: a real attack that an analyst should look at
  - **false_positive**: the alert fired on harmless traffic and can be closed
- The dataset's Attack label maps to `escalate` and Non-Attack to `false_positive`.
- Jev sees the alert as recorded: rule name, alert type, kill-chain stage, and the request and response. The label is left out, and so are the source and destination IPs and source port, which SecAlertBench replaced with random values.
- The instructions describe what each answer means; they don't hint which one is right. The answer definitions are in `criteria.json`.
- Two ways to ask, as in the tax eval: one choice between the two answers (default), or one yes/no "should this be escalated?" question that escalates at 0.5 or above (`-protocol noul`).
- Alerts go in batches of 500, or fewer if `TYPESAFE_MAX_ITEMS` is lower.

## 3. Score it

- **Attacks caught.** This is the main number, because a missed attack is the costly mistake.
- **False alarms**: how many harmless alerts it escalates, which is what wears analysts out.
- Overall accuracy and F1.
- A **cheat baseline**: guess each alert's label from the other alerts under the same rule name. If a model can't beat that, it isn't reading the alert. It's strong: 92.8% accuracy, 88.3% of attacks caught, 5.1% false alarms. It's also a ceiling a zero-shot model can't really reach, since it learns from the eval set's own labels.
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

## Watch out for

- The README doesn't say who labeled the alerts. We trust the labels but spot-check 100 of them.
- "false_positive" includes what looks like sanctioned internal scanning. About half the Log4j alerts labeled false_positive carry real `${jndi:...}` payloads from one internal address. No single alert can show it was authorized, so some misses there are expected.
