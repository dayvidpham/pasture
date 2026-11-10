---
name: user-uat
description: User Acceptance Testing with demonstrative examples
---

# User Acceptance Test (UAT)

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
**Command:** `pasture:user:uat` — User Acceptance Testing with demonstrative examples

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-5-plan-uat)** <- Phase 5 (Plan UAT) and Phase 11 (Impl UAT)

**[uat-demonstrative-examples]**
- Given: reviewers reach consensus
- When: conducting UAT
- Then: show demonstrative examples
- Should not: ask abstract questions

**[uat-one-component-at-a-time]**
- Given: UAT questions
- When: asking
- Then: present one component at a time with definition + implementation + example BEFORE asking
- Should not: dump all questions at once about all components simultaneously

**[uat-real-alternatives]**
- Given: UAT questions
- When: forming options
- Then: describe specific tradeoffs and design choices made
- Should not: use generic approval options like 'exactly matches', 'mostly matches', 'requires revisions'

**[uat-verbatim-feedback]**
- Given: user feedback
- When: storing
- Then: record verbatim with context
- Should not: paraphrase concerns

**[uat-plan-revise]**
- Given: user rejects
- When: plan UAT
- Then: return to proposal phase
- Should not: proceed to implementation

**[uat-impl-revise]**
- Given: user rejects
- When: impl UAT
- Then: return to relevant slice
- Should not: proceed to landing

**[uat-open-ended-alongside]**
- Given: component questions
- When: presenting
- Then: ALWAYS include an open-ended feedback question alongside the ACCEPT/REVISE decision so the user can raise related concerns
- Should not: present only the ACCEPT/REVISE decision without a free-text feedback opportunity

**[uat-update-urd]**
- Given: UAT completes
- When: results are captured
- Then: update the URD with UAT results via `"${EPOCH_REC}" note "${URD_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "UAT: <summary>"`
- Should not: leave the URD out of date after UAT

**[uat-feedback-disposition]**
- Given: each item of feedback or proposed deferral (deferrals may be flagged by the user OR proposed by the architect/supervisor) gathered during UAT (Phase 5 or Phase 11)
- When: recording it
- Then: assign every item an explicit, user-confirmed disposition of FIX-NOW or DEFER and echo it back for confirmation; ALL deferred items — whoever proposed them — MUST be raised to the user at the next user gate (URE, Plan UAT, or Impl UAT) for confirmation; FIX-NOW items are resolved in the current wave, DEFER'd items are the SOLE source feeding the FOLLOWUP epic
- Should not: leave a feedback item without a confirmed disposition; silently defer any item without raising it to the user at the next gate; route any review severity (BLOCKER/IMPORTANT/MINOR) into FOLLOWUP — only DEFER'd UAT items feed it

**[uat-invoke-skill]**
- Given: the Phase 5 or Phase 11 UAT interview
- When: conducting it
- Then: MUST invoke `skill(/pasture:user-uat)` so the verbatim-capture, FIX-NOW/DEFER disposition, and validation-case confirmation procedures are loaded
- Should not: conduct the UAT without invoking its skill — skipping it loses the disposition and verbatim-capture procedures

**[frag--validation-cases]**
- Given: any REQUEST (every request, not only fix-intent ones)
- When: eliciting (URE), acceptance-testing (UAT), or implementing
- Then: elicit concrete validation cases — a definition of done plus correct and incorrect behaviours (inputs/behaviors that must pass or must fail), confirm the case set with the user in UAT, evaluate the implementation against them, and store failing real-data cases as test fixtures
- Should not: ship without validation cases; treat validation cases as applying to fix-intent requests only; introduce a request-type axis or enum to gate them

## Acceptance Template

Ground the question in an earlier turn, ask one decision, and record the user’s words verbatim.

## Task Recovery

The tracker backend is recorded once in .agents.local/pasture/EPOCH.md; read that file instead of re-probing. If EPOCH.md is absent, stop and ask the orchestrator to run detection.

Recover from live tracker state at session start and after compaction; never trust cached task rows. Read the `rec:` path from the header and use it as the one entry point: `"${EPOCH_REC}" <op>`. The operation set is closed: create, label, block, note, show, query, ready, status, close, whoami, signal.

Store selection: the adapter reads PASTURE_DB_PATH when set, otherwise the header's `db:`; the probe wrote that value from the same precedence (XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db). Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

The adapter passes `--namespace` only when EPOCH.md records `namespace:`. That pin wins on both backends. Otherwise it omits the flag. Pasture then derives the namespace itself: PASTURE_NAMESPACE, else the git remote, then file:// of the working directory. The ledger calls derive_namespace for the header and for every write that has no pin. That function is the one list of remote forms; do not restate it. With no origin, both backends use file:// of the working directory, so invocations from different subdirectories use different namespaces. Queries without a pin follow that backend's derivation; pass `--all-namespaces` to opt out. When neither a pin nor PASTURE_NAMESPACE is set, pasture reads are unscoped and creation falls back to the git remote, then file:// of the working directory. Inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run `"${EPOCH_REC}" ready`, `"${EPOCH_REC}" query --blocked`, and `"${EPOCH_REC}" query --status in_progress`. For each referenced active task, run `"${EPOCH_REC}" show "$TASK_URI"`, `"${EPOCH_REC}" show "$TASK_URI" --notes`, and `"${EPOCH_REC}" show "$TASK_URI" --history`. All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the URI create printed; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create prints the new task URI directly. Persist it before any label or note: create-plus-label is non-atomic, so on failure retain the URI and retry only the failed label/note operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

An owner is recorded with `"${EPOCH_REC}" label "$TASK_URI" owner:<role>`. That is a claim, not an allocation: an unassigned task stays unassigned until the supervisor allocates it, and repeating an identical claim is not allocation. Never invent an owner.

Parent stays open and is blocked by child: `"${EPOCH_REC}" block "$PARENT_URI" --by "$CHILD_URI"`. Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

If the header records `backend: error`, stop before the first mutation: the store exists but cannot be used, and falling back could lose earlier epochs. A mid-epoch write failure retries 3 times. After the third failure, write the attempted record to .agents.local/pasture/pending/<timestamp>-<op>.md, keep the recorded backend, and surface the pending record to the user at the next gate. Never switch backends and never drop the record.

## Task Attribution

Select the current registered author with "${EPOCH_REC}" whoami. It prints the author recorded in the header or PASTURE_AUTHOR, and on the pasture backend it confirms that actor with agents show. It does not list agents and it does not invent one. If neither source names an author, it stops: set author: in EPOCH.md or export PASTURE_AUTHOR. Never auto-register or guess an identity during recovery. Every note supplies --author explicitly; machine examples use the pre-registered pasture-system/00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

## Writing to the User

These rules adapt ASD-STE100 Issue 9, Part 1 to chat messages. They are STE-aligned. The standard's Part 2 dictionary is not adopted, so no message can claim full compliance. This fragment covers wording: plain terms, short sentences, active voice, no invented labels, and the wording house rule, plus the pre-send check and the status template. Five skills carry these wording rules: architect-handoff, status, supervisor, user-elicit, user-uat.

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

No self-praise is the wording house rule.

| Rule | Requirement |
|---|---|
| No self-praise | No self-congratulation. State the next step in one line instead. |

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
2. Does every term the reader did not introduce appear in Terms before first use?
3. Search the draft for a token that matches `[A-Z]{1,3}-?[0-9]`, for a section sign (`§`), and for a five- or six-character token that matches `[A-Za-z0-9]{5,6}` and contains both a letter and a digit. Is each one defined, or replaced by its substance?
4. Does one item have two names, or one word have two senses?
5. Any sentence over 20 words (instruction or question) or 25 (description)?
6. Any noun cluster over 3 words? Any slang verb, noun-as-verb, or phrasal verb?
7. Any semicolon or em-dash?
8. Any passive verb with no actor?
9. Does an irreversible action carry WARNING or CAUTION?
10. Delete one sentence. If nothing is lost, keep it deleted.

## Question and Option Design

This fragment covers question and option design: grounding before asking, one decision, option shape, recommendation placement, the present template, and the weak-tool degrade. Wording rules, the STE-aligned claim, the status template, and the pre-send check are in Writing to the User. Four skills carry these question rules: architect-handoff, supervisor, user-elicit, user-uat. The status skill asks no question, so it does not carry this fragment. Ask through the `request_user_decision` operation, never by launching a subagent. That launch is `delegate_assignment`, a different operation. The role-tool binding in `internal/runtime/profiles.go` lowers `request_user_decision` to the harness question tool and lowers `delegate_assignment` to the subagent tool.

### The rules

Format: number, requirement, the STE rule it adapts, then a compact bad-to-good pair.

**Grounding**

| # | Rule | Adapts | Bad to good |
|---|---|---|---|
| 2 | Ground before you ask. Show what the thing is, where it lives, and one example. Never put the only explanation in the options. | 6.1, 4.4 | 'Which order: the types to the gate in parallel, or the gate first?' to 'Three parts share one type. The gate reads it. The fixtures copy it. Code: `ir.Event`.' |

**Questions and options**

Rules 22, 23 and 24 are house rules. The house-rule table names them by number. Do not attach an STE number to a house rule.

| # | Rule | Adapts | Bad to good |
|---|---|---|---|
| 22 | One question, one decision. Say what the answer changes. | house | 'Which order do you want, and are the labels clear?' to 'Which part merges first? The answer sets the order for the next 3 days.' |
| 23 | Each option is self-contained: a label of max 6 words, what happens, and the cost. Use only Terms words. | house | 'Both' to 'Both limits. The import stops at the first limit it reaches. Cost: one more setting to test.' |
| 24 | Put the recommendation outside the options, with one reason. You may also mark the label '(recommended)'. | house | An option labelled 'recommended' with no reason, to 'Recommendation: merge the event types first, because a later change costs rework in two places.' |

### House rules for questions, options and recommendations

ASD-STE100 has no rule for questions, options, recommendations, or the turn split around a question tool. Rules 2, 22, 23 and 24 already carry grounding before asking, one decision per question, option design and recommendation placement. Four rows in this table are pointers to those rules. Question shape and Text-before-question turn add only what those rules do not. No self-praise is a wording rule; it is in Writing to the User. Never cite an STE number for a house rule.

| Rule | Requirement |
|---|---|
| Question shape | A short header, max 4 words. |
| Option design | Extends rule 23. Do not restate it. |
| Recommendation placement | Extends rule 24. Do not restate it. |
| Grounding before asking | Extends rule 2. Do not restate it. |
| One decision per question | Extends rule 22. Do not restate it. |
| Text-before-question turn | Send the grounding as text and end the turn. Ask through the `request_user_decision` operation in the next turn, because a question tool can hide prose sent in the same turn. |

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

### Degrade when the question tool is weak

Some hosts expose a weak question tool: it takes one question per call, it has no multi-select, or its picker hides option descriptions. Degrade in this order:

1. Ask one question.
2. Phrase the options as a single-select choice.
3. State the assumption you will use if the answer is unavailable.

Put the assumption in the text turn that grounds the question, not inside the options. An assumption never satisfies a user gate: a gated phase waits for the user's answer. The question still asks for one decision, and the recommendation names the cost of the assumption.

### Pre-send check for a question

Run this list before you send a question. It tests the question rules in this fragment. The wording pre-send check is in Writing to the User.

1. Before a question: did the reader already see what the thing is, its definition, and one example, in an earlier turn or an earlier block?
2. Can the reader choose from the options alone, with no other document? Is the recommendation outside the options?

## UAT Phases



### Plan UAT (Phase 5 — pasture:p5-user:s5-uat)

After 3 reviewers ACCEPT the proposal, present each major design decision to the user one at a time. For each component:
1. Show the proposed interface definition (code snippet)
2. Show a motivating example (how a user would use it)
3. Ask about the specific design choices made (tradeoffs, alternatives considered)

### Implementation UAT (Phase 11 — pasture:p11-user:s11-uat)

After code review consensus, demonstrate what was actually built component by component. For each component:
1. Run the actual command / show real output
2. Compare against the original proposal
3. Ask about the specific behavioral decisions made in the implementation

## How to Structure UAT Questions

**Critical:** Questions must split the engineering design space on its ambiguous boundaries to extract maximum information — like a decision tree, where each question bisects the remaining uncertainty.

The user needs to see the actual thing — definition, behavior, example — and then evaluate the specific engineering tradeoffs at the boundaries where the design could go either way.

### Question Design Principles

1. **Each question targets one ambiguous boundary.** Identify where in the design space two or more viable alternatives exist, and ask the user to choose.
2. **Options describe competing tradeoffs, not approval levels.** Each option is a real engineering alternative with its own pros/cons.
3. **Later questions depend on earlier answers.** Structure the survey as a decision tree — Round 1 settles the highest-leverage boundaries, Round 2 addresses dependent decisions informed by Round 1 answers, etc.
4. **Show context before asking.** The user MUST see a code snippet, interface definition, or motivating before/after example before being asked to evaluate.
5. **One component per question call.** Never batch all components into one survey.

## Wrong vs Right Question Patterns

**WRONG — generic approval (DO NOT USE):**
```
"Does this match your vision?"
options: ["Yes exactly", "Mostly yes", "Partially", "No"]
```

These fail because the options don't represent engineering alternatives.

**RIGHT — boundary-splitting design decisions:**
```
"The verbose flag adds the following fields to each log entry. Which fields are most useful?"
options based on actual fields implemented, e.g.:
  - "session ID on every transcript event — adds noise but enables correlation"
  - "backupDir on backup events — confirms where files land"
  - "repo path + hash on sync events — confirms which repo was detected"
  - "full key=value dump for unknown events — good for debugging"

"We sanitize emails in file paths by replacing @ with _ and non-alphanumeric chars with _.
 Which sanitization behavior is correct?"
options based on real alternatives:
  - "@ → _AT_ (reversible, unambiguous)"
  - "@ → _ (current behavior, ambiguous if username contains _)"
  - "keep @ (valid on most filesystems except Windows)"
  - "base64-encode the email (fully reversible, opaque)"
```

These work because each option is a real engineering alternative with clear tradeoffs.

## Pre-requisite: Cross-reference URE Against the Proposal

UAT is the **second time** the user evaluates this feature. Before designing UAT questions, cross-reference the URE responses and URD against the proposal:

```bash
"${EPOCH_REC}" show "${ELICIT_ID_URI}"     # Re-read the user's original URE responses
"${EPOCH_REC}" show "${URD_ID_URI}"        # The structured requirements document
"${EPOCH_REC}" show "${PROPOSAL_ID_URI}"   # The architect's proposal and tradeoffs
```

Look for:
- **Faithful translations:** Where the proposal directly implements a URE choice — confirm it matches their intent
- **Tradeoffs the architect resolved:** Where the URE left ambiguity and the architect chose one direction — surface the choice and its rationale
- **New dimensions the proposal introduced:** Engineering concerns that weren't in the URE — present these with context
- **Gaps or drift:** Where the proposal may have diverged from, reinterpreted, or dropped a URE requirement — flag these explicitly

## Question Sequence (Decision Tree)

Structure questions to progressively validate the proposal against the user's original requirements.

**Round 1: Highest-leverage tradeoffs** (one decision per question call)

Start with the 2-3 architectural decisions where the proposal made the biggest choices. For each, show the user:
1. What they originally said in URE (their stated requirement/preference)
2. What the proposal chose (the concrete interface, type, or approach)
3. The alternatives that were considered and why this one was picked

**Round 2: Dependent and derivative decisions** (informed by Round 1)

With the major tradeoffs validated, surface the second-order decisions that flow from them.

**Round 3: New dimensions not in URE** (if any)

Present engineering concerns that emerged after URE (from research, codebase exploration, or reviewer feedback).

**Final: Catch-all**

One open-ended question — "Is there anything from your original requirements that you don't see reflected in this proposal?"

## Component-at-a-Time Pattern



### Step 1: Show the definition and motivating example

```
Present the interface/type definition (e.g., the TypeScript type or function signature)
Then show a concrete before/after or input/output example:

  BEFORE this change:  $ aura watch --follow
  [23:24:20] Updated: session-abc123
  → Backed up 3 files

  AFTER this change: $ aura watch --follow --verbose
  [23:24:20] Updated: -home-minttea-dev.../session-abc123
    path: /home/minttea/.claude/projects/...
    session: abc123
    → Repo sync: enqueued (debounced)
```

### Step 2: Ask about specific decisions

Design-space questions to ask per component type:

**For output/display decisions:**
- Which fields are useful vs. noise at the default verbosity level?
- Which fields belong only in verbose mode?

**For data model / type decisions:**
- Should this be statically defined (enum) or dynamic (string)?
- Should the schema be strict (reject unknown fields) or loose (allow extra)?

**For behavioral/algorithm decisions:**
- Should this fail fast or recover silently?
- Should side effects be eager (immediate) or lazy (deferred/debounced)?

**For API/interface decisions:**
- Is the flag name/command name intuitive?
- Does the default behavior match expectations?

## UAT Survey Template

Use one question call per decision. One call asks one question. Do not put two decisions in one call, and do not batch every component into one survey.

```
question({
  questions: [
    {
      question: `The verbose flag shows the following extra lines for backup events:
  backupDir: /home/user/.aura/aura-sync/repo/project/provider/claude/session/abc123
  session: abc123
Which of these verbose fields are useful?`,
      header: "Verbose fields",
      multiSelect: true,
      options: [
        { label: "backupDir (full path)", description: "Shows where the backup actually landed" },
        { label: "session ID", description: "Enables log correlation across events" },
      ]
    }
  ]
})

question({
  questions: [
    {
      question: "Any related feedback, concerns, or gaps not covered above?",
      header: "Feedback",
      multiSelect: false,
      options: [
        { label: "No additional feedback", description: "All concerns addressed" },
        { label: "Related concern", description: "I have feedback on something adjacent" },
      ]
    }
  ]
})

question({
  questions: [
    {
      question: "Do you ACCEPT this component to proceed?",
      header: "Decision",
      multiSelect: false,
      options: [
        { label: "ACCEPT", description: "Proceed to next component" },
        { label: "REVISE", description: "Needs changes before proceeding" }
      ]
    }
  ]
})
```

## Per-Item Disposition: FIX-NOW or DEFER

Every piece of UAT feedback — whether a REVISE on a component or an open-ended concern — MUST be given an explicit, **user-confirmed** disposition. There is no third option.

| Disposition | Meaning | Where it goes |
|-------------|---------|----------------|
| **FIX-NOW** | Resolve before this wave closes | Back to the architect (Plan UAT) or the relevant slice (Impl UAT) |
| **DEFER** | Acceptable to ship now; track for later | The **sole** source feeding the FOLLOWUP epic |

**Echo each disposition back to the user for confirmation** before recording it — e.g. "Recording '{{feedback}}' as DEFER (tracked in follow-up, not fixed this wave) — confirm?". Record the confirmed disposition verbatim alongside the feedback.

**Deferrals may be proposed by the architect or supervisor**, not only flagged by the user. Whoever proposes a deferral, **all deferred items MUST be raised to the user at the next user gate** (URE, Plan UAT, or Impl UAT) for confirmation — nothing is silently deferred. At each gate, present the complete outstanding deferral set and let the user confirm or override each item before it is recorded as DEFER.

Review severities (BLOCKER / IMPORTANT / MINOR) are **never** dispositioned here and **never** feed FOLLOWUP — they are resolved to zero during code review (Phase 10). FOLLOWUP is fed by DEFER'd UAT items only.

## Validation-Case Confirmation (EVERY request)

For **every** REQUEST (not only fix-intent ones — see the URE validation cases), UAT MUST close the validation-case loop:
1. **Show the user the validation case set** elicited in URE (the definition of done plus the correct/incorrect behaviours that must pass or must fail; for fix-intent requests this includes the inputs/behaviors that were failing).
2. **Confirm the set is complete and correct** — ask whether any case is missing or wrong; capture additions verbatim.
3. **Demonstrate the implementation evaluated against each case** (Impl UAT: run them; Plan UAT: confirm the proposal covers them).
4. The failing real-data cases become stored test fixtures.

Do NOT mark a component ACCEPT without confirming its validation cases pass.

## Creating UAT Task



### Plan UAT Task (Phase 5)

```bash
UAT_ID_URI=$("${EPOCH_REC}" create --kind task --title "UAT: Plan acceptance for <feature>" --phase plan_uat \
  --body "---
references:
  request: "${REQUEST_ID_URI}"
  urd: "${URD_ID_URI}"
  proposal: "${PROPOSAL_ID_URI}"
---
## Components Reviewed

### Component: <component-name>
**Definition shown:** <interface/type/signature shown to user>
**Motivating example shown:** <before/after or input/output example>
**Question asked:** <exact question text>
**Options presented:** <exact option labels and descriptions>
**User response:** <verbatim selection(s)>
**Disposition:** <FIX-NOW or DEFER — user-confirmed, echoed back>

## Final Decision
<ACCEPT or REVISE with verbatim reason>")
"${EPOCH_REC}" label "$UAT_ID_URI" pasture:p5-user:s5-uat

"${EPOCH_REC}" block "${PROPOSAL_ID_URI}" --by "${UAT_ID_URI}"

# Update URD with plan UAT results
"${EPOCH_REC}" note "${URD_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Plan UAT: <ACCEPT or REVISE> - <summary of key decisions>"
```

### Implementation UAT Task (Phase 11)

```bash
IMPL_UAT_ID_URI=$("${EPOCH_REC}" create --kind task --title "UAT: Implementation acceptance for <feature>" --phase impl_uat \
  --body "---
references:
  request: "${REQUEST_ID_URI}"
  urd: "${URD_ID_URI}"
  impl_plan: "${IMPL_PLAN_ID_URI}"
---
## Components Demonstrated

### Component: <component-name>
**Command run / output shown:** <actual terminal output shown to user>
**Question asked:** <exact question>
**User response:** <verbatim response>
**Disposition:** <FIX-NOW or DEFER — user-confirmed, echoed back>

## Final Decision
<ACCEPT or REVISE>")
"${EPOCH_REC}" label "$IMPL_UAT_ID_URI" pasture:p11-user:s11-uat

"${EPOCH_REC}" block "${IMPL_PLAN_ID_URI}" --by "${IMPL_UAT_ID_URI}"

# Update URD with implementation UAT results
"${EPOCH_REC}" note "${URD_ID_URI}" --author pasture-system/00000000-0000-0000-0000-000000000000 --body "Impl UAT: <ACCEPT or REVISE> - <summary of findings>"
```

## Handling REVISE

If user selects REVISE:
- **Plan UAT:** Return to architect for proposal revision on the specific component
- **Impl UAT:** Return to relevant slice for implementation fixes

Document the specific component and the user's verbatim feedback in the task description. Do not generalize.

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

On the pasture backend, the epoch creates one feedback task at epoch start and writes `feedback: <task URI>` into `.agents.local/pasture/EPOCH.md`. A second run reads the `feedback:` line and does not create another task. On the ledger backend, create nothing and record nothing: there is no feedback task and no `feedback:` line. No other skill creates a feedback task. If the pasture line is absent, the signal operation fails and records nothing; do not create a task to fill the gap.

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
