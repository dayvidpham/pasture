---
name: status
description: Project status and monitoring via Pasture queries
---

# Pasture Status

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
**Command:** `pasture:status` — Project status and monitoring via Pasture queries

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md)**

## Task Recovery

The tracker backend is recorded once in .agents.local/pasture/EPOCH.md; read that file instead of re-probing. If EPOCH.md is absent, stop and ask the orchestrator to run detection.

Recover from live tracker state at session start and after compaction; never trust cached task rows. Read the `rec:` path from the header and use it as the one entry point: `"${EPOCH_REC}" <op>`. The operation set is closed: create, label, block, note, show, query, ready, status, close, whoami, signal.

Store selection: the adapter reads PASTURE_DB_PATH when set, otherwise the header's `db:`; the probe wrote that value from the same precedence (XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db). Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

The adapter passes `--namespace` only when EPOCH.md records `namespace:`. That pin wins on both backends. Otherwise it omits the flag. Pasture then derives the namespace itself: PASTURE_NAMESPACE, else the git remote, then file:// of the working directory. The ledger calls one derivation function for the header and for every write that has no pin; it handles git@, ssh://, git://, http://, https:// and user@host:path. Queries without a pin follow that backend's derivation; pass `--all-namespaces` to opt out. When neither a pin nor PASTURE_NAMESPACE is set, pasture reads are unscoped and creation falls back to the git remote, then file:// of the working directory. Inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run `"${EPOCH_REC}" ready`, `"${EPOCH_REC}" query --blocked`, and `"${EPOCH_REC}" query --status in_progress`. For each referenced active task, run `"${EPOCH_REC}" show "$TASK_URI"`, `"${EPOCH_REC}" show "$TASK_URI" --notes`, and `"${EPOCH_REC}" show "$TASK_URI" --history`. All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the URI create printed; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create prints the new task URI directly. Persist it before any label or note: create-plus-label is non-atomic, so on failure retain the URI and retry only the failed label/note operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

An owner is recorded with `"${EPOCH_REC}" label "$TASK_URI" owner:<role>`. That is a claim, not an allocation: an unassigned task stays unassigned until the supervisor allocates it, and repeating an identical claim is not allocation. Never invent an owner.

Parent stays open and is blocked by child: `"${EPOCH_REC}" block "$PARENT_URI" --by "$CHILD_URI"`. Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

If the header records `backend: error`, stop before the first mutation: the store exists but cannot be used, and falling back could lose earlier epochs. A mid-epoch write failure retries 3 times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.

## Task Attribution

Select the current registered author with "${EPOCH_REC}" whoami. It prints the author recorded in the header or PASTURE_AUTHOR, and on the pasture backend it confirms that actor with agents show. It does not list agents and it does not invent one. If neither source names an author, it stops: set author: in EPOCH.md or export PASTURE_AUTHOR. Never auto-register or guess an identity during recovery. Every note supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

## Writing to the User

These rules adapt ASD-STE100 Issue 9, Part 1 to chat messages. They are STE-aligned. The standard's Part 2 dictionary is not adopted, so no message can claim full compliance. This fragment covers wording: plain terms, short sentences, active voice, and no invented labels, plus the pre-send check. Question and option design is in Question and Option Design; this fragment does not repeat it. Five skills carry these wording rules: architect-handoff, status, supervisor, user-elicit, user-uat.

### The rules

Format: number, requirement, the STE rule it adapts, then a compact bad-to-good pair.

**Shape**

| # | Rule | Adapts | Bad to good |
|---|---|---|---|
| 1 | Lead with the result, the decision, or the blocker. | 6.1, 6.2 | 'This pins both the edge and the core case per branch, so a same-count swap fails.' to 'Result: the test now fails if a real case is replaced with a filler case.' |
| 3 | One topic per paragraph. Max 3 sentences. | 6.5, 6.6 | One 25-word sentence with four stacked ideas, to three sentences with one idea each. |
| 4 | For 3 or more items, a flow, or a comparison, use a list, a table, or an ASCII diagram. | 4.3 | 'Adopt the first option, then the second, then both' to a three-row table: option, what happens, cost. |
| 5 | Cut filler, self-narration, hedging, praise, and closing offers. | house | 'It is important to note that this change effectively serves to ensure the test can detect' to 'The test detects a removed case.' |

**Terms**

| # | Rule | Adapts | Bad to good |
|---|---|---|---|
| 6 | Put every term the user did not introduce in a Terms block before first use. Max 20 words per definition. Include the code name. | 1.1, 1.6 | 'per arm' to 'Arm: one of the 5 branches of the skip gate. Code: `arm`.' |
| 7 | Use the industry term for the concept. Do not invent a term. | 1.8, 1.10 | 'a same-count swap' to 'a replacement that keeps the test count the same'. |
| 8 | One name per item, in the message and across the session. | 1.11, 9.4 | 'the gate', then 'the event types', then 'the IR', to 'the event types' each time. |
| 9 | One meaning per word. Do not use one word in two senses. | 1.2, 1.3 | 'pins' for a version lock and 'pins' for a test assertion, to 'locks the version' and 'the test checks'. |
| 10 | No internal shorthand in prose. Write the substance first, then the reference in parentheses. | 1.10, 2.2, 8.6 | 'Adopt the option code and skip the explanation' to 'Adopt the retry limit (task: Importer retry limit, round 6).' |
| 11 | Max 3 words in a noun cluster, and no stack of hidden ideas. A hyphenated stack still hides several. Break it with 'of', 'that', or a clause. | 2.1, 1.9 | 'mutation-load-bearing core case' to 'the core case (the one case per branch that fails when the branch breaks)'. |
| 12 | Write each abbreviation in full once, then the short form in parentheses. | 2.2, 8.3 | 'the continuous integration job, then the CI job, with no first full form' to 'the continuous integration (CI) job, then the CI job.' |
| 13 | Use plain verbs. No slang, no noun-as-verb, no phrasal verb. | 1.7, 1.10, 9.3 | 'reddens' to 'fails'. |

**Sentences**

| # | Rule | Adapts | Bad to good |
|---|---|---|---|
| 14 | Max 20 words for an instruction or a question. Max 25 for a description. An identifier or a quote counts as one word. The cap is necessary, not sufficient. | 5.1, 6.3, 8.6 | 'The test checks ten cases, and because each branch needs two named cases and the count is no longer enough, a replacement can pass.' to 'The test checks 10 cases. A filler case can no longer pass.' |
| 15 | Active voice. Name the actor: the test, CI, the agent, the merge. Simple tenses only. | 3.2, 3.6 | 'The gate will be rebased on the types.' to 'The agent rebases the gate on the event types.' |
| 16 | One instruction per sentence. Imperative. Condition first. | 5.2, 5.3, 5.4 | 'All three at once, then check.' to 'If the types change, rebase the gate. Then run the tests.' |
| 17 | Full sentences keep articles and verbs. Fragments only in lists, tables, and labels. | 4.2, 4.3, 4.5 | 'Defer the timeout, keep the retry limit fix' to 'Keep the retry limit. Defer the timeout change. The correctness review is round 3 and the test-quality review is round 2.' |
| 18 | No semicolons. No em-dashes. Use a period or a list. | 8.1 + house | 'per arm, so a swap fails' with an em-dash, to 'per arm. That is 10 checks, so a swap fails.' |
| 19 | Show the logic with because, so, but, if, then. | 4.4 | '10 predicates. A same-count swap fails.' to 'The test checks 10 cases, so a replacement that keeps the count cannot pass.' |

**Exempt material**

| # | Rule | Adapts | Bad to good |
|---|---|---|---|
| 20 | Backtick each identifier. Max 2 per sentence. Give a plain gloss on first use. | 8.6 + house | Four bare references in one line, to one task, one design decision, and two reviews, each written in substance first. |
| 21 | Quote user text, code, paths, and logs exactly. Explain them outside the quote. | 8.6 | A quote silently edited, to the blockquote verbatim with a plain reading on the next line. |

**Risk**

| # | Rule | Adapts | Bad to good |
|---|---|---|---|
| 25 | Mark an irreversible action with WARNING or CAUTION, then the condition, then the result. | 7.1, 7.2, 7.3 | 'Removing the core case will fail the suite.' to 'CAUTION: if you remove the core case, the test passes and a broken branch ships.' |

### Wording house rule

No self-praise is the wording house rule. The question house rules are in Question and Option Design.

| Rule | Requirement |
|---|---|
| No self-praise | No self-congratulation. State the next step in one line instead. |

### Exempt material

Two material classes are exempt from the word and sentence caps: quoted text and code blocks. A third is allowed as technical nouns. Everything else obeys the caps.

- **Quoted user text.** Verbatim in a blockquote. Never rewrite it into the house style. Put the plain reading after the quote.
- **Code, logs, diffs, test output.** Exempt. Explain them in prose around the block.
- **Industry terms of art.** Fixture, golden test, mutation testing, schema migration, fail-safe default, backfill, and closed enum are allowed as technical nouns. Define one in Terms if the user did not use it first.

Two more rows are relaxations, not exemptions. **Spelling** keeps one spelling per message and does not enforce American spelling. **Identifiers** are not exempt from a gloss, as rule 20 says.

The caps stay live for everything else. Rule 3 sets paragraph length, rule 14 sets the word caps, and rule 17 sets where a fragment is allowed; none of those three is an exemption.

### Pre-send check

Run this list on every user-facing message. A clean pass is not a gate.

1. Does sentence one give the result, the decision, or the blocker?
2. Before a question: did the reader already see what the thing is, its definition, and one example, in an earlier turn or an earlier block?
3. Does every term the reader did not introduce appear in Terms before first use?
4. Search the draft for a token that matches `[A-Z]{1,3}-?[0-9]`, for a section sign (`§`), and for a five- or six-character token that matches `[A-Za-z0-9]{5,6}` and contains both a letter and a digit. Is each one defined, or replaced by its substance?
5. Does one item have two names, or one word have two senses?
6. Any sentence over 20 words (instruction or question) or 25 (description)?
7. Any noun cluster over 3 words? Any slang verb, noun-as-verb, or phrasal verb?
8. Any semicolon or em-dash?
9. Any passive verb with no actor?
10. Can the reader choose from the options alone, with no other document? Is the recommendation outside the options?
11. Does an irreversible action carry WARNING or CAUTION?
12. Delete one sentence. If nothing is lost, keep it deleted.

## Steps



### 1. Check for active plans

```bash
"${EPOCH_REC}" query --label="pasture:p3-plan:s3-propose" --status=open
"${EPOCH_REC}" query --label="pasture:p6-plan:s6-ratify" --status=open
```

### 2. Check implementation progress

```bash
"${EPOCH_REC}" query --label="pasture:p8-impl:s8-plan" --status=open
"${EPOCH_REC}" query --label="pasture:p9-impl:s9-slice" --status=in_progress
"${EPOCH_REC}" query --blocked --label="pasture:p9-impl:s9-slice"
"${EPOCH_REC}" query --label="pasture:p9-impl:s9-slice" --status=closed
```

### 3. Get project stats

```bash
"${EPOCH_REC}" query | python3 -c 'import collections,json,sys; print(dict(collections.Counter(x["status"] for x in json.load(sys.stdin))))'
```

### 4. Report status

Summarize findings across plans, implementation, and blocked tasks in the output format below.

## Output Format

```
## Pasture Protocol Status

**Phase:** {Phase 1: Request | Phase 3: Propose | Phase 4: Review | Phase 6: Ratified | Phase 9: Implementation}
**Active Plan:** {task-id or "None"}

### Plans
- ["${PROPOSAL_ID_URI}"] Status: {open|closed}
- [ratified-id] Status: {open|closed}

### Implementation Progress
- IMPL_PLAN: {task-id}
- Layer 1: {N}/{M} complete
- Layer 2: {N}/{M} complete (blocked: {count})

### Blocked Tasks
- {task-id}: {blocker reason}

### Recent Activity
"${EPOCH_REC}" query
```

## Quick Status

```bash
"${EPOCH_REC}" query | python3 -c 'import collections,json,sys; print(dict(collections.Counter(x["status"] for x in json.load(sys.stdin))))'
"${EPOCH_REC}" ready
"${EPOCH_REC}" query --blocked
```
<!-- END GENERATED FROM pasture schema -->
