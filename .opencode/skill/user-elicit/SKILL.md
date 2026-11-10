---
name: user-elicit
description: User Requirements Elicitation survey (Phase 2)
---

# User Requirements Elicitation (Phase 2)

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
**Command:** `pasture:user:elicit` — User Requirements Elicitation survey (Phase 2)

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-2-elicit--urd-aurap2-user)** <- Phase 2

**[user-elicit-plan-backwards]**
- Given: user request captured
- When: eliciting
- Then: plan backwards from end vision to MVP
- Should not: start proposal without elicitation

**[user-elicit-read-phase1]**
- Given: Phase 1 complete
- When: starting elicitation
- Then: read Phase 1 outputs (classification, research findings, explore findings) from REQUEST task comments to scope URE questions
- Should not: ignore prior art discoveries or codebase exploration results

**[user-elicit-multiselect]**
- Given: elicitation questions
- When: asking
- Then: use multiSelect: true for flexibility
- Should not: force single-choice answers

**[user-elicit-verbatim-responses]**
- Given: responses captured
- When: storing
- Then: record questions AND answers verbatim (including all options presented)
- Should not: summarize user responses or omit option text

**[user-elicit-chain-dep]**
- Given: elicitation complete
- When: creating task
- Then: chain dependency to request task
- Should not: skip dependency

**[user-elicit-urd-reference]**
- Given: URD created
- When: linking to other tasks
- Then: include URD ID in description frontmatter of referencing tasks
- Should not: use `"${EPOCH_REC}" block --blocked-by` for URD links (URD is a reference document, not a blocking dependency)

**[user-elicit-code-shown]**
- Given: any definition, code snippet, interface, or before/after example shown to the user during elicitation (e.g. in the preview the question call shows)
- When: recording the Q&A
- Then: capture the shown definition/code VERBATIM in the elicit task alongside the question and response (parity with UAT's 'Definition shown' / 'Command run' fields)
- Should not: record only the answer while dropping the code or definition the user was reacting to — the response is meaningless without what was shown

**[user-elicit-invoke-skill]**
- Given: the Phase 2 URE interview
- When: conducting it
- Then: MUST invoke `skill(/pasture:user-elicit)` so the verbatim-capture and validation-case elicitation procedures are loaded
- Should not: conduct the URE without invoking its skill — skipping it loses verbatim capture and the validation-case lifecycle

**[user-elicit-raise-deferrals]**
- Given: deferred items outstanding from a prior phase (flagged by the user OR proposed by the architect/supervisor)
- When: conducting this URE gate (a user gate)
- Then: ALL deferred items, whoever proposed them, MUST be raised to the user at the next user gate (URE, Plan UAT, or Impl UAT) for confirmation — present the complete outstanding deferral set and let the user confirm or override each item; nothing is silently deferred
- Should not: silently carry a deferral forward without raising it to the user at this gate

**[frag--validation-cases]**
- Given: any REQUEST (every request, not only fix-intent ones)
- When: eliciting (URE), acceptance-testing (UAT), or implementing
- Then: elicit concrete validation cases — a definition of done plus correct and incorrect behaviours (inputs/behaviors that must pass or must fail), confirm the case set with the user in UAT, evaluate the implementation against them, and store failing real-data cases as test fixtures
- Should not: ship without validation cases; treat validation cases as applying to fix-intent requests only; introduce a request-type axis or enum to gate them

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

These rules adapt ASD-STE100 Issue 9, Part 1 to chat messages. They are STE-aligned. The standard's Part 2 dictionary is not adopted, so no message can claim full compliance. Five skills carry these rules: architect-handoff, status, supervisor, user-elicit, user-uat. Ask through the `request_user_decision` operation, never by launching a subagent. That launch is `delegate_assignment`, a different operation. The role-tool binding in `internal/runtime/profiles.go` lowers `request_user_decision` to the harness question tool and lowers `delegate_assignment` to the subagent tool.

### The rules

Format: number, requirement, the STE rule it adapts, then a compact bad-to-good pair.

**Shape**

| # | Rule | Adapts | Bad to good |
|---|---|---|---|
| 1 | Lead with the result, the decision, or the blocker. | 6.1, 6.2 | 'This pins both the edge and the core case per branch, so a same-count swap fails.' to 'Result: the test now fails if a real case is replaced with a filler case.' |
| 2 | Ground before you ask. Show what the thing is, where it lives, and one example. Never put the only explanation in the options. | 6.1, 4.4 | 'Which order: the types to the gate in parallel, or the gate first?' to 'Three parts share one type. The gate reads it. The fixtures copy it. Code: `ir.Event`.' |
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

**Questions, options, risk**

Rules 22 to 24 are house rules. The house-rule table names them by number. Do not attach an STE number to a house rule.

| # | Rule | Adapts | Bad to good |
|---|---|---|---|
| 22 | One question, one decision. Say what the answer changes. | house | 'Which order do you want, and are the labels clear?' to 'Which part merges first? The answer sets the order for the next 3 days.' |
| 23 | Each option is self-contained: a label of max 6 words, what happens, and the cost. Use only Terms words. | house | 'Both' to 'Both limits. The import stops at the first limit it reaches. Cost: one more setting to test.' |
| 24 | Put the recommendation outside the options, with one reason. You may also mark the label '(recommended)'. | house | An option labelled 'recommended' with no reason, to 'Recommendation: merge the event types first, because a later change costs rework in two places.' |
| 25 | Mark an irreversible action with WARNING or CAUTION, then the condition, then the result. | 7.1, 7.2, 7.3 | 'Removing the core case will fail the suite.' to 'CAUTION: if you remove the core case, the test passes and a broken branch ships.' |

### House rules for questions, options and recommendations

ASD-STE100 has no rule for questions, options, recommendations, or the turn split around a question tool. Rules 2, 22, 23 and 24 already carry grounding, one decision, option design and recommendation placement. Four rows in this table are pointers to those rules. The other three rows add only what those rules do not. Never cite an STE number for a house rule.

| Rule | Requirement |
|---|---|
| Question shape | A short header, max 4 words. |
| Option design | Extends rule 23. Do not restate it. |
| Recommendation placement | Extends rule 24. Do not restate it. |
| Grounding before asking | Extends rule 2. Do not restate it. |
| One decision per question | Extends rule 22. Do not restate it. |
| Text-before-question turn | Send the grounding as text and end the turn. Ask through the `request_user_decision` operation in the next turn, because a question tool can hide prose sent in the same turn. |
| No self-praise | No self-congratulation. State the next step in one line instead. |

### Template: present a component, then ask

Send the block below as text only, then end the turn. In the next turn, ask through the `request_user_decision` operation with the same options. The split is the Text-before-question turn house rule.

~~~~markdown
## <Part name>: <what it does for the user, max 10 words>

**Decision needed:** <one sentence>. The answer changes <what>.

**Terms**
- **<term>**: <max 20 words>. Code: `<identifier>`.
- **<term>**: <max 20 words>.

**What it is**
<max 3 sentences. Name the actor. One fact per sentence.>

**Definition**
~~~<lang>
<the smallest type, interface, schema, or command shape that shows the choice>
~~~

**Example**
~~~
<one realistic before and after, or input and output>
~~~

**Diagram**
~~~
<ASCII, plain labels, no em-dash>
~~~

**Options**

| Option | What happens | Cost |
|---|---|---|
| <label, max 6 words> | <one sentence> | <one sentence> |
| <label> | <one sentence> | <one sentence> |

**Recommendation:** <option>, because <one reason>.
~~~~

Then, in the next turn:

~~~
`request_user_decision`: '<question, max 20 words>'
options: the same labels, with the 'what happens' and 'cost' text repeated in each description, because the picker can hide the table.
~~~

One component per call. Use multi-select only when the options combine. Always add one open-ended feedback question.

### Template: status update

~~~markdown
**Status:** <the result or the state, max 25 words>.

**Done**
- <artifact or outcome, past tense, actor named> (`<path>`, pull request #N, or task title).

**Next**
- <what happens next, and who does it>.

**Needs you** (omit if empty)
- <the decision>, needed for <what it unblocks>.

**Risk** (omit if empty)
- WARNING or CAUTION: <condition>. <result>.

**Terms** (new terms only)
- **<term>**: <max 20 words>.
~~~

Budget: 120 words. Every Done item names an artefact. Do not restate the plan. One status per message, not one per thought.

### Degrade when the question tool is weak

Some hosts expose a weak question tool: it takes one question per call, it has no multi-select, or its picker hides option descriptions. Degrade in this order:

1. Ask one question.
2. Phrase the options as a single-select choice.
3. State the assumption you will use if the answer is unavailable.

Put the assumption in the text turn that grounds the question, not inside the options. An assumption never satisfies a user gate: a gated phase waits for the user's answer. The question still asks for one decision, and the recommendation names the cost of the assumption.

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

## Sub-steps

| Sub-step | Label | Description |
|----------|-------|-------------|
| s2_1-elicit | `pasture:p2-user:s2_1-elicit` | URE survey — structured requirements elicitation |
| s2_2-urd | `pasture:p2-user:s2_2-urd` (also `pasture:urd`) | Create URD — single source of truth for requirements |

## Elicitation Strategy (s2_1)



### 1. End Vision (Plan Backwards)

Ask about the user's ultimate goal and what interfaces they envision:
- What does the final feature look like?
- How will users interact with it?
- What other systems need to integrate?

### 2. MVP Scope (Plan Forward)

Jump to the starting point:
- What's the minimum viable version?
- What can be deferred to later iterations?
- What are the must-have vs nice-to-have features?

### 3. Engineering Dimensions

Ask targeted questions to map the problem space:
- Parallelism: Can operations run concurrently?
- Distribution: Single process or distributed?
- Scale: How many users/requests/items?
- Has-a / Is-a relationships in the domain

### 4. Boundaries and Constraints

- Performance requirements?
- Security considerations?
- Compatibility constraints?
- Error handling expectations?

### 5. Catch-All

Final question to capture anything missed.

### 6. Validation Cases (EVERY request)

Elicit **concrete validation cases** during this URE — for **every** request, not only fix-intent ones. We always need to know what "done" means, and what correct vs incorrect behaviour looks like:
- The **definition of done** — what observable outcome means the request is satisfied.
- The exact inputs/behaviors that MUST PASS (the expected correct output).
- The exact inputs/behaviors that MUST FAIL or are explicitly out of scope (incorrect behaviour). For fix-intent requests this includes the inputs/behaviors that currently FAIL (the bug as the user observes it).
- Any real data, commands, or reproduction steps the user can provide — capture these **verbatim**.

These cases seed the request's test fixtures and are the set confirmed with the user in UAT (`/pasture:user-uat`) and evaluated against the implementation. Do NOT introduce a `request-type` enum to gate this — what a request needs is recognized semantically.

### Pre-requisite: Read Phase 1 Outputs

Before designing URE questions, **read all Phase 1 outputs** (classification,
research findings, codebase exploration) from the REQUEST task and its comments.
These narrow the design space and reveal which boundaries are already clear vs
which need user input.

```bash
"${EPOCH_REC}" show "${REQUEST_ID_URI}"   # Read classification + research + explore findings
```

Use the Phase 1 findings to identify:
- Which engineering dimensions are **already decided** (don't ask about these)
- Which dimensions have **multiple viable alternatives** (ask about these)
- Which dimensions the user **may not have considered** (surface these)

### Question Sequence (Decision Tree)

Structure questions as a decision tree that progressively narrows the design
space. Each question should depend on the answers to previous questions.

**Round 1: Highest-leverage boundaries** (one decision per question call)

Identify the 2-3 dimensions that most constrain the design. These are the axes
where different choices lead to fundamentally different architectures.

Ask one component at a time. Show the user:
1. The concrete thing being decided (code snippet, interface, diagram)
2. A motivating example of how each option plays out
3. The tradeoffs between options

**Round 2: Dependent decisions** (informed by Round 1 answers)

With the high-level architecture settled, ask about the next layer of decisions
that were ambiguous.

**Round 3: Edge cases and constraints** (if needed)

Remaining questions about error handling, performance targets, compatibility
requirements — but only where the answer isn't obvious from prior context.

**Final: Catch-all**

One open-ended question to capture anything the decision tree missed.

## Example Survey

```
question(questions: [
  {
    question: "What is your end vision for this feature? How will users interact with it when complete?",
    header: "End Vision",
    multiSelect: true,
    options: [
      { label: "Simple UI control", description: "Button/link users click" },
      { label: "Automated process", description: "Happens without user action" },
      { label: "API endpoint", description: "Programmatic access" },
      { label: "Background service", description: "Runs continuously" }
    ]
  },
  {
    question: "What is the minimum viable version (MVP) that would be useful?",
    header: "MVP Scope",
    multiSelect: true,
    options: [
      { label: "Core functionality only", description: "Just the basic action" },
      { label: "With confirmation", description: "User confirms before action" },
      { label: "With feedback", description: "Show success/error state" },
      { label: "Full featured", description: "All bells and whistles" }
    ]
  },
  {
    question: "Are there any specific constraints or requirements?",
    header: "Constraints",
    multiSelect: true,
    options: [
      { label: "Performance critical", description: "Must be fast" },
      { label: "Security sensitive", description: "Handles sensitive data" },
      { label: "Backwards compatible", description: "Can't break existing" },
      { label: "No constraints", description: "Flexible implementation" }
    ]
  },
  {
    question: "Is there anything else we should know about this feature?",
    header: "Other",
    multiSelect: true,
    options: [
      { label: "Related to existing feature", description: "Connects to something" },
      { label: "Inspired by another product", description: "Has a reference" },
      { label: "Urgent timeline", description: "Needed soon" },
      { label: "Nothing else", description: "Covered everything" }
    ]
  }
])
```

## Creating the Elicit Task (s2_1)

After survey completion, capture the full Q&A record using the same structured
format as [UAT_TEMPLATE.md](../protocol/UAT_TEMPLATE.md). Each question must
include the exact question text, ALL options with their descriptions, and the
user's verbatim response. When a definition, code snippet, or example was shown
to the user before a question, capture it verbatim in a **Definition/code shown:**
field (parity with UAT's 'Definition shown' / 'Command run' fields). For
**every** request, also record the elicited validation cases verbatim.

```bash
ELICIT_ID_URI=$("${EPOCH_REC}" create --kind task --title "ELICIT: {{feature name}}" --phase elicit \
  --body "---
references:
  request: "${REQUEST_ID_URI}"
---
## Questions and Responses

### End Vision
Q: What is your end vision for this feature? How will users interact with it when complete?
Definition/code shown: {{verbatim definition/snippet shown to user, or 'none'}}
Options: Simple UI control (Button/link users click), Automated process (Happens without user action), API endpoint (Programmatic access), Background service (Runs continuously)
A: {{user's verbatim selections and any custom input}}

### MVP Scope
Q: What is the minimum viable version (MVP) that would be useful?
Options: Core functionality only (Just the basic action), With confirmation (User confirms before action), With feedback (Show success/error state), Full featured (All bells and whistles)
A: {{user's verbatim selections}}

### Constraints
Q: Are there any specific constraints or requirements?
Options: Performance critical (Must be fast), Security sensitive (Handles sensitive data), Backwards compatible (Can't break existing), No constraints (Flexible implementation)
A: {{user's verbatim selections}}

### Other
Q: Is there anything else we should know about this feature?
Options: Related to existing feature (Connects to something), Inspired by another product (Has a reference), Urgent timeline (Needed soon), Nothing else (Covered everything)
A: {{user's verbatim input}}

## Validation Cases (EVERY request)
- Definition of done: {{verbatim observable outcome that means the request is satisfied}}
- Must pass (correct behaviour): {{verbatim expected correct behavior}}
- Must fail / out of scope (incorrect behaviour): {{verbatim — for fix-intent, the input/behavior that fails today}}
- Repro / real data: {{verbatim commands, data, or steps — or 'none'}}")
"${EPOCH_REC}" label "$ELICIT_ID_URI" pasture:p2-user:s2_1-elicit
"${EPOCH_REC}" note "$ELICIT_ID_URI" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Requested role (not an assignment): architect"

# Chain dependency: REQUEST blocked by ELICIT
"${EPOCH_REC}" block "${REQUEST_ID_URI}" --by "${ELICIT_ID_URI}"
```

## Creating the URD (s2_2)

After the elicit task is created, create the URD as the single source of truth for user requirements:

```bash
URD_ID_URI=$("${EPOCH_REC}" create --kind task --title "URD: {{feature name}}" --phase elicit \
  --body "---
references:
  request: "${REQUEST_ID_URI}"
  elicit: "${ELICIT_ID_URI}"
---
## Requirements
{{structured requirements extracted from URE survey}}

## Priorities
{{user-stated priorities from survey responses}}

## Design Choices
{{design decisions surfaced during elicitation}}

## MVP Goals
{{minimum viable scope identified}}

## End-Vision Goals
{{user's ultimate vision for the feature}}")
"${EPOCH_REC}" label "$URD_ID_URI" pasture:urd
"${EPOCH_REC}" label "$URD_ID_URI" pasture:p2-user:s2_2-urd
```

The URD is a **reference document**, not a blocking dependency. Other tasks reference it via description frontmatter (`urd: "${URD_ID_URI}"`), not via blocking dependency commands.

Record the URD task ID — pass it to the architect for Phase 3.

## Next Phase

After elicitation and URD creation, invoke `/pasture:architect` to begin proposal creation (Phase 3). Pass the URD ID so the architect can reference it.

The proposal task will block the elicit task:
```bash
"${EPOCH_REC}" block "${ELICIT_ID_URI}" --by "${PROPOSAL_ID_URI}"
```

## Signal Capture

Record the user's friction or approval when it appears, so a later project-level skill can distill it. Capture is a side effect of work already happening: it never prompts the user and never blocks a phase.

### Closed kind list

The kind is a closed list. Use exactly one of:

- `frustration`: the user shows annoyance, repeats a complaint, or rejects the interaction itself.
- `positive-signal`: the user endorses a direction or a result before a gate asks.
- `satisfaction`: the user accepts a gate and says the result is what they wanted.
- `confusion`: the user asks what a term means, misreads a question, or answers a different question.
- `correction`: the user corrects a fact, a requirement, or an earlier answer.

### One feedback task per epoch

The epoch creates one feedback task at epoch start and writes `feedback: <task URI>` into `.agents.local/pasture/EPOCH.md`. A second run finds that task by reading the `feedback:` line. No other skill creates a feedback task. If the line is absent, the signal operation fails and records nothing; do not create a task to fill the gap.

### Line format and recording

Write one JSON object per signal. The fields, in this order, are kind, phase, skill, entry and quote.

~~~
{"kind":"<kind>","phase":"<phase>","skill":"<skill>","entry":"<entry-ref>","quote":"<short verbatim quote>"}
~~~

- `kind`: one of the registered kinds above, exactly.
- `phase`: one of request, elicit, propose, review, plan-review, ratify, handoff, impl-plan, worker-slices, code-review, impl-uat, landing.
- `skill`: the skill that recorded it, named exactly as the skill directory.
- `entry`: the record that holds the interaction, such as a task URI plus the question or section.
- `quote`: a short verbatim fragment, max 20 words. Never paraphrase or tidy it.

Record the signal with one call. Do not name a backend command in this section.

~~~bash
"${EPOCH_REC}" signal --kind confusion --phase elicit --skill user-elicit --entry '<task URI>#end-vision' --quote 'what does arm mean here'
~~~

The adapter records that JSON object. An empty feedback id fails in the adapter and records nothing. Write the call in the same turn as the work. Do not ask permission to record. Do not wait for the write before you continue the phase.

If the write fails, follow the tracker write-failure rule: retry, keep the recorded backend, and surface the pending line at the next gate. A failed record never blocks the phase and never prompts the user.
<!-- END GENERATED FROM pasture schema -->
