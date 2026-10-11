# UAT Structured Output Template

Use this XML template to capture UAT results. Each component is presented one at a time to the user with its definition, motivating example, and design-space questions.

All task reference placeholders contain full task URIs from tracker reads or
create JSON `.id`. Use the namespace/store/registered-author bindings in
[PROCESS.md](PROCESS.md#tracker-setup-and-recipe-bindings).

## Template

```xml
<uat>
  <metadata>
    <title>{{UAT_TITLE}}</title>
    <task-uri>{{UAT_TASK_URI}}</task-uri>
    <proposal-ref>{{PROPOSAL_TASK_URI}}</proposal-ref>
    <phase>{{plan | implementation}}</phase>
    <date>{{YYYY-MM-DD}}</date>
  </metadata>

  <components>

    <component id="{{N}}">
      <name>{{COMPONENT_NAME}}</name>

      <definition>
        <code lang="{{LANGUAGE}}">
{{CODE_SNIPPET_OR_INTERFACE_DEFINITION}}
        </code>
      </definition>

      <motivating-example>
        {{DESCRIPTION_OF_BEFORE_AFTER_OR_USAGE_SCENARIO}}
        <code lang="{{LANGUAGE}}">
{{EXAMPLE_CODE_OR_OUTPUT}}
        </code>
      </motivating-example>

      <questions>

        <question id="Q{{M}}">
          <text>{{EXACT_QUESTION_TEXT_PRESENTED_TO_USER}}</text>
          <context>{{OPTIONAL_ADDITIONAL_CONTEXT_SHOWN_BEFORE_OPTIONS}}</context>

          <options>
            <option id="{{A}}">
              <label>{{OPTION_LABEL}}</label>
              <description>{{OPTION_DESCRIPTION_WITH_TRADEOFFS}}</description>
            </option>
            <option id="{{B}}">
              <label>{{OPTION_LABEL}}</label>
              <description>{{OPTION_DESCRIPTION_WITH_TRADEOFFS}}</description>
            </option>
            <!-- 2-4 options per question -->
          </options>

          <user-response>
            <selected>{{OPTION_LABEL_OR_OTHER}}</selected>
            <verbatim>{{USER_RESPONSE_VERBATIM_INCLUDING_NOTES}}</verbatim>
            <!-- Per-item disposition: FIX-NOW (resolve this wave) or DEFER
                 (sole FOLLOWUP source). User-confirmed, echoed back. -->
            <disposition>{{FIX-NOW | DEFER}}</disposition>
          </user-response>
        </question>

        <!-- User-initiated comments (not in response to a question) -->
        <user-comment>
          <verbatim>{{EXACT_USER_COMMENT}}</verbatim>
          <disposition>{{FIX-NOW | DEFER}}</disposition>
        </user-comment>

      </questions>

      <decision>{{ACCEPT | REVISE}}</decision>
      <decision-notes>{{OPTIONAL_NOTES_ON_DECISION}}</decision-notes>
    </component>

  </components>

  <addenda>
    <!-- Post-UAT user comments captured verbatim -->
    <addendum>
      <verbatim>{{EXACT_USER_STATEMENT}}</verbatim>
      <design-implication>{{WHAT_THIS_MEANS_FOR_THE_DESIGN}}</design-implication>
      <disposition>{{FIX-NOW | DEFER}}</disposition>
    </addendum>
  </addenda>

  <final-decision>
    <verdict>{{ACCEPT | REVISE}}</verdict>
    <summary>
      <change id="{{N}}">{{KEY_DESIGN_CHANGE_FROM_ORIGINAL_PROPOSAL}}</change>
      <!-- One per design change -->
    </summary>
    <open-questions>
      <question>{{DEFERRED_OR_UNRESOLVED_QUESTION}}</question>
    </open-questions>
  </final-decision>

</uat>
```

## Pasture Task Commands

```bash
# 1. Create the UAT task with structured description
#    - Frontmatter references the proposal; label identifies the UAT phase
#    - Description contains the full XML-structured UAT record
UAT_URI=$(pasture task create "UAT: {{PLAN_OR_IMPL}} acceptance for {{FEATURE}}" --phase plan_uat \
  --namespace "$PASTURE_NAMESPACE" --format json \
  --description "---
references:
  proposal: $PROPOSAL_URI
  urd: $URD_URI
  request: $REQUEST_URI
---
<FULL_XML_STRUCTURED_DESCRIPTION>" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
# Plan UAT; for implementation use --phase impl_uat above and pasture:p11-user:s11-uat below
pasture task label add "$UAT_URI" pasture:p5-user:s5-uat
# Retain UAT_URI if labeling fails; retry labels without creating another task.

# 2. Record post-UAT addenda as comments (user feedback after main survey)
pasture task comment add "$UAT_URI" "UAT ADDENDUM (user-initiated, verbatim): {{VERBATIM}}" --author "$AUTHOR"

# 3. Update URD with UAT results
pasture task comment add "$URD_URI" "UAT {{PHASE}}: {{VERDICT}} - {{key design decisions summary}}" --author "$AUTHOR"

# 4. After UAT passes, proceed to ratification (Phase 6)
pasture task label add "$PROPOSAL_URI" pasture:p6-plan:s6-ratify
pasture task comment add "$PROPOSAL_URI" "RATIFIED: All 3 reviewers ACCEPT, UAT passed ($UAT_URI)." --author "$AUTHOR"

# 5. If UAT fails (REVISE), return to proposal phase
#    - Do NOT ratify
#    - Create a new PROPOSAL-N task (increment N) related to the original
#    - Record the specific component and verbatim feedback
```

## Rules

1. **One component per AskUserQuestion call** — never batch all components into one survey
2. **Show definition + example BEFORE asking** — user must see the concrete thing first
3. **Options describe tradeoffs, not approval levels** — never use "exactly matches" / "mostly matches"
4. **Capture verbatim** — user responses are recorded exactly as given, including notes
5. **Option descriptions are part of the record** — capture label AND description for each option presented
6. **User-initiated comments** go in `<user-comment>` outside the question/answer flow
7. **Addenda** capture post-UAT feedback that modifies the design
8. **Final decision** summarizes all design changes relative to the proposal being UAT'd
9. **Binary decisions only** — component decisions are ACCEPT or REVISE (no intermediate levels)
10. **Findings tracked via severity tree** — if ACCEPT with minor concerns, track them as IMPORTANT/MINOR findings in the severity tree during code review (Phase 10), not at UAT time
11. **Per-item disposition** — every feedback item gets a user-confirmed, echoed-back `FIX-NOW` or `DEFER`. Deferrals may be flagged by the user OR proposed by the architect/supervisor; whoever proposes one, **all deferred items MUST be raised to the user at the next user gate** (URE, Plan UAT, or Impl UAT) for confirmation — nothing is silently deferred. FIX-NOW resolves this wave; DEFER is the **sole** source feeding the FOLLOWUP epic. Review severities (BLOCKER/IMPORTANT/MINOR) are never dispositioned here and never feed FOLLOWUP.

## See Also

- [UAT_EXAMPLE.md](./UAT_EXAMPLE.md) — Complete worked example of this template in use
