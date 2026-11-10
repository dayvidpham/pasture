---
name: architect-handoff
description: Create handoff document and transfer to supervisor
---

# Architect: Handoff to Supervisor

<!-- BEGIN GENERATED FROM pasture schema -->
<!-- GENERATED FROM pasture v0.0.32 artifact v1 -->
**Command:** `pasture:architect:handoff` — Create handoff document and transfer to supervisor

**-> [Full workflow in PROCESS.md](../protocol/PROCESS.md#phase-7-handoff)** <- Phase 7

**[arch-handoff-link-proposal]**
- Given: ratified PROPOSAL-N task
- When: handing off
- Then: author the handoff in a HANDOFF Pasture task body, linking to the ratified proposal
- Should not: hand off without linking to ratified proposal

**[arch-handoff-spawn-supervisor]**
- Given: handoff for the IMPL_PLAN phase
- When: spawning the supervisor
- Then: delegate the supervisor as the implementation-plan owner (workers also on the frontier tier), then give it a self-contained assignment
- Should not: spawn the supervisor without a self-contained assignment for the IMPL_PLAN phase

**[arch-handoff-supervisor-not-idle]**
- Given: a freshly spawned supervisor
- When: it dispatches Explore subagents and appears idle
- Then: let it work — an apparently-idle supervisor is usually running Explore subagents to map the codebase before decomposing slices
- Should not: shut down or restart a supervisor that looks idle at the start of the IMPL_PLAN phase

**[arch-handoff-no-impl-tasks]**
- Given: implementation planning
- When: handing off
- Then: let supervisor create vertical slice tasks
- Should not: create implementation tasks as architect

## Task Recovery

The tracker backend is recorded once in .agents.local/pasture/EPOCH.md; read that file instead of re-probing. If EPOCH.md is absent, stop and ask the orchestrator to run detection.

Recover from live tracker state at session start and after compaction; never trust cached task rows. Read the `rec:` path from the header and use it as the one entry point: `"${EPOCH_REC}" <op>`. The operation set is closed: create, label, block, note, show, query, ready, status, close, whoami, signal.

Store selection: the adapter reads PASTURE_DB_PATH when set, otherwise the header's `db:`; the probe wrote that value from the same precedence (XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db). Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

The adapter passes `--namespace` only when EPOCH.md records `namespace:`. That pin wins on both backends. Otherwise it omits the flag. Pasture then derives the namespace itself: PASTURE_NAMESPACE, else the git remote, then file:// of the working directory. The ledger calls derive_namespace for the header and for every write that has no pin. That function is the one list of remote forms; do not restate it. A ledger with no origin uses file:// of the git work-tree root, so a subdirectory does not split the namespace. Queries without a pin follow that backend's derivation; pass `--all-namespaces` to opt out. When neither a pin nor PASTURE_NAMESPACE is set, pasture reads are unscoped and creation falls back to the git remote, then file:// of the working directory. Inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

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

## When to Use

Plan ratified and user has approved proceeding with implementation.

## Handoff Template

Storage: the handoff is authored directly in the **HANDOFF Pasture task body** (no filesystem path; the task body IS the handoff).

```markdown
# Handoff: Architect → Supervisor

## Supervisor Startup
1. Call `skill(/pasture:supervisor)` to load your role instructions
2. Spawn ephemeral Explore subagents with the task tool when codebase exploration is needed
3. Read the RATIFIED PROPOSAL and URD with `"${EPOCH_REC}" show` commands below
4. Every vertical slice MUST have leaf tasks — any number, named after the real work units (the L1 types / L2 tests / L3 impl triple is only illustrative)

## References
- REQUEST: "${REQUEST_ID_URI}"
- URD: "${URD_ID_URI}" (read with `"${EPOCH_REC}" show "${URD_ID_URI}"`)
- RATIFIED PROPOSAL: "${RATIFIED_PROPOSAL_ID_URI}" (read with `"${EPOCH_REC}" show "${RATIFIED_PROPOSAL_ID_URI}"`)

## Summary
<1-2 sentence summary of what needs to be implemented>

## Key Files
<list main files to be created/modified from the ratified plan>

## Validation Checklist
<validation checklist from the ratified proposal>

## BDD Acceptance Criteria
<Given/When/Then criteria from the ratified plan>

## Implementation Notes
<any special considerations, known risks, or constraints>
```

## Steps

1. Create the HANDOFF Pasture task — its body IS the handoff document (use the template above):
   ```bash
   HANDOFF_ID_URI=$("${EPOCH_REC}" create --kind task --title "HANDOFF: Architect → Supervisor for REQUEST" --phase handoff --priority 2 \
     --body="---
   references:
     request: "${REQUEST_ID_URI}"
     urd: "${URD_ID_URI}"
     proposal: "${RATIFIED_PROPOSAL_ID_URI}"
   ---
   # Handoff: Architect → Supervisor
   <full handoff body per the template above>")
   "${EPOCH_REC}" label "$HANDOFF_ID_URI" pasture:p7-plan:s7-handoff

   ```

2. Delegate the supervisor with the task operation (the IMPL_PLAN phase runs under the supervisor):
   ```
   delegate the implementation-plan owner, then give it the self-contained assignment below
   ```

3. Monitor supervisor progress:
   ```bash
   # Check pasture status
   "${EPOCH_REC}" query --status=in_progress
   ```

   A supervisor that looks idle right after spawn is usually running Explore subagents — do **not** shut it down pre-emptively.

## Example Prompt

**CRITICAL:** The send_assignment_message assignment MUST instruct the supervisor to invoke `/pasture:supervisor` as its first action. Without this, the supervisor agent starts without its role instructions and skips leaf task creation, ephemeral exploration, and other critical procedures.

```
Start by calling `skill(/pasture:supervisor)` to load your role instructions.

Implement the ratified plan for <feature name>.

## Context
- REQUEST: "${REQUEST_ID_URI}"
- URD: "${URD_ID_URI}" (read with `"${EPOCH_REC}" show "${URD_ID_URI}"` for user requirements)
- RATIFIED PROPOSAL: "${RATIFIED_PROPOSAL_ID_URI}"
- HANDOFF: "${HANDOFF_ID_URI}" (the handoff body — read with `"${EPOCH_REC}" show "${HANDOFF_ID_URI}"`)

## Summary
<1-2 sentence summary of what needs to be implemented>

## Key Files
<list main files to be created/modified from the ratified plan>

## Acceptance Criteria
<Given/When/Then criteria from the ratified plan>

## Reminders
1. Call `skill(/pasture:supervisor)` FIRST — do not proceed without loading your role
2. Spawn ephemeral Explore subagents with the task tool when codebase exploration is needed
3. Every vertical slice MUST have leaf tasks — any number, named after the real work units (the L1/L2/L3 triple is only illustrative); a slice without leaf tasks is undecomposed
4. Read the ratified plan with `"${EPOCH_REC}" show "${RATIFIED_PROPOSAL_ID_URI}"` and the URD with `"${EPOCH_REC}" show "${URD_ID_URI}"`
```

Deliver this assignment to the supervisor when you delegate it:

```
send_assignment_message({
  to: "supervisor",
  message: `Start by calling skill(/pasture:supervisor) to load your role instructions.

Implement the ratified plan for User Authentication.

## Context
- REQUEST: ${REQUEST_ID_URI}
- URD: "${URD_ID_URI}"
- RATIFIED PROPOSAL: "${RATIFIED_PROPOSAL_ID_URI}"
- HANDOFF: "${HANDOFF_ID_URI}" (read with "${EPOCH_REC}" show "${HANDOFF_ID_URI}")

## Summary
Add JWT-based authentication with login/logout endpoints and middleware.

## Key Files
- pkg/auth/jwt.go
- pkg/auth/middleware.go
- cmd/api/auth.go

## Acceptance Criteria
Given a valid JWT token when accessing protected routes then allow access
Given an expired token when accessing protected routes then return 401

## Reminders
1. Call skill(/pasture:supervisor) FIRST
2. Spawn ephemeral Explore subagents with the task tool when codebase exploration is needed
3. Every slice MUST have leaf tasks (any number; L1/L2/L3 is only illustrative)
4. Read ratified plan: "${EPOCH_REC}" show "${RATIFIED_PROPOSAL_ID_URI}" and URD: "${EPOCH_REC}" show "${URD_ID_URI}"`,
  summary: "IMPL_PLAN assignment with Pasture context"
})
```

## Delegation Notes

- Delegate the supervisor (and the workers it will coordinate) as **T1 frontier** assignments — the IMPL_PLAN phase benefits from the frontier tier for decomposition and review.
- A delegated assignment has **zero prior context**: every assignment MUST be self-contained (call `skill(/pasture:supervisor)`, the Pasture task IDs, and `"${EPOCH_REC}" show` commands to fetch full requirements).
- Do not spawn the supervisor for the IMPL_PLAN phase except through this handoff.

## IMPORTANT

- **DO NOT** skip the handoff and spawn the supervisor for the IMPL_PLAN phase — delegate it with the task operation
- **DO NOT** create implementation tasks yourself - the supervisor creates vertical slice tasks
- **DO NOT** implement the plan yourself - your role is handoff and monitoring
- **DO NOT** shut down a supervisor that appears idle right after spawn — it is usually running Explore subagents
- The supervisor reads the ratified plan and determines vertical slice structure
- Architect monitors for blockers or escalations

## Follow-up Lifecycle (h1 Reuse)

This handoff (h1: Architect → Supervisor) also occurs after FOLLOWUP_PROPOSAL is ratified. In follow-up context:

- **Storage:** the follow-up handoff is authored in its own HANDOFF Pasture task body (no filesystem path)
- **References:** Include both original URD and FOLLOWUP_URD task IDs
- **Context:** Summary of FOLLOWUP_PROPOSAL ratification and the user-DEFER'd UAT items the follow-up addresses
- **Next step:** Supervisor creates the follow-up plan and the follow-up slices for that scope
<!-- END GENERATED FROM pasture schema -->
