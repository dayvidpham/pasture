---
name: user-elicit
description: User Requirements Elicitation survey (Phase 2)
---

# User Requirements Elicitation (Phase 2)

<!-- BEGIN GENERATED FROM pasture schema -->
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
- Should not: use `pasture task dep add --blocked-by` for URD links (URD is a reference document, not a blocking dependency)

**[user-elicit-code-shown]**
- Given: any definition, code snippet, interface, or before/after example shown to the user during elicitation (e.g. in an AskUserQuestion preview)
- When: recording the Q&A
- Then: capture the shown definition/code VERBATIM in the elicit task alongside the question and response (parity with UAT's 'Definition shown' / 'Command run' fields)
- Should not: record only the answer while dropping the code or definition the user was reacting to — the response is meaningless without what was shown

**[user-elicit-invoke-skill]**
- Given: the Phase 2 URE interview
- When: conducting it
- Then: MUST invoke `Skill(/pasture:user-elicit)` so the verbatim-capture and validation-case elicitation procedures are loaded
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

Recover from live Pasture state at session start and after compaction; never trust cached task rows.

Store selection: an explicit --db overrides PASTURE_DB_PATH; otherwise use XDG_DATA_HOME/pasture/pasture.db, HOME/.local/share/pasture/pasture.db, then .pasture/pasture.db. Use the same selected store for every command. If a recovery query fails, report its actual store path, failed operation, impact, and permission/schema/configuration repair; failure is not an empty work queue.

Set PASTURE_NAMESPACE to the repository's canonical namespace URI, for example https://github.com/dayvidpham/pasture. Explicit --namespace overrides the git-remote-derived namespace, then file:// of the working directory. List requires an explicit namespace to avoid mixing repositories. ready/blocked have only an exact --label filter, not namespace filtering; inspect the returned full URI before choosing repository work. Separate reads are not an atomic snapshot.

Run pasture task ready, pasture task blocked, and pasture task list --namespace "$PASTURE_NAMESPACE" --status in_progress. For each referenced active task, run pasture task show "$TASK_URI", pasture task comments "$TASK_URI", and pasture task timeline "$TASK_URI". All *_URI variables in examples are inputs bound to full task URIs from an assignment, a verified tracker read, or the actual JSON id returned by create; never use legacy short IDs. Resolve each variable before execution and quote it as one operand.

Create returns an object whose id is the task URI: capture it with --format json and python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])'. Persist that URI before label/comment steps. Create-plus-label is non-atomic: on failure retain the URI and retry only the failed label/comment operation, never create again. Only open, in_progress, and closed are statuses; blockage is represented by live dependencies and notes, not a blocked status.

Assignment transfer is not initial allocation: before using pasture task assignment transfer, obtain the existing owner-responsibility assignment and its exact successor assignment ID, registered committing actor, and registered worker occupant from the supervisor. If the task is unassigned, stop and let the supervisor arrange allocation; no assignment-start command is implied. Never repeat an identical transfer as a substitute for allocating different workers.

Parent stays open and is blocked by child: pasture task dep add "$PARENT_URI" --blocked-by "$CHILD_URI". Reference documents belong in description frontmatter under references:, never fabricated blockers. Workers report evidence and handoffs without closing their slices or leaves; the supervisor closes only after independent review and satisfied gates. Use git agent-commit for local commits. Never install, enable, or modify Git hooks, Git hook path configuration, or pre-commit integration without explicit user approval. Tracker writes are durable; Git history lands separately.

## Task Attribution

Select the current registered author with pasture task agents list and pasture task agents show ACTOR-ID. Never auto-register or guess an identity during recovery. Every comment supplies --author explicitly; machine examples use the pre-registered pasture-system--00000000-0000-0000-0000-000000000000 actor. If it is absent, stop and request registration from the operator rather than claiming registration is fixed. Preserve human intent verbatim and quote historical attribution as evidence, not as a forged new author.

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
pasture task show "${REQUEST_ID_URI}"   # Read classification + research + explore findings
```

Use the Phase 1 findings to identify:
- Which engineering dimensions are **already decided** (don't ask about these)
- Which dimensions have **multiple viable alternatives** (ask about these)
- Which dimensions the user **may not have considered** (surface these)

### Question Sequence (Decision Tree)

Structure questions as a decision tree that progressively narrows the design
space. Each question should depend on the answers to previous questions.

**Round 1: Highest-leverage boundaries** (1-2 questions per AskUserQuestion call)

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
AskUserQuestion(questions: [
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
ELICIT_ID_URI=$(pasture task create "ELICIT: {{feature name}}" --phase elicit --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
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
- Repro / real data: {{verbatim commands, data, or steps — or 'none'}}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$ELICIT_ID_URI" pasture:p2-user:s2_1-elicit
pasture task update "$ELICIT_ID_URI" --notes "Requested role (not an assignment): architect"

# Chain dependency: REQUEST blocked by ELICIT
pasture task dep add "${REQUEST_ID_URI}" --blocked-by "${ELICIT_ID_URI}"
```

## Creating the URD (s2_2)

After the elicit task is created, create the URD as the single source of truth for user requirements:

```bash
URD_ID_URI=$(pasture task create "URD: {{feature name}}" --phase elicit --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
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
{{user's ultimate vision for the feature}}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
pasture task label add "$URD_ID_URI" pasture:urd
pasture task label add "$URD_ID_URI" pasture:p2-user:s2_2-urd
```

The URD is a **reference document**, not a blocking dependency. Other tasks reference it via description frontmatter (`urd: "${URD_ID_URI}"`), not via blocking dependency commands.

Record the URD task ID — pass it to the architect for Phase 3.

## Next Phase

After elicitation and URD creation, invoke `/pasture:architect` to begin proposal creation (Phase 3). Pass the URD ID so the architect can reference it.

The proposal task will block the elicit task:
```bash
pasture task dep add "${ELICIT_ID_URI}" --blocked-by "${PROPOSAL_ID_URI}"
```
<!-- END GENERATED FROM pasture schema -->
