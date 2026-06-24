# Merge Request Template

A blank, reusable skeleton for authoring a merge/pull request description. Copy
the section below into your MR/PR body and fill each section in. For a fully
worked example, see [EXAMPLE_MR_DESCRIPTION.md](EXAMPLE_MR_DESCRIPTION.md).

Keep every section: if one does not apply, write `N/A` and a one-line reason
rather than deleting it — reviewers rely on the fixed shape.

---

## Summary
<!-- What does this MR do, and why? One short paragraph: the purpose and the
     user-facing outcome. Link the originating REQUEST/URD if applicable. -->

## Testing
- [ ] Created tests
- [ ] Updated tests
- [ ] Tested via integration / simulation
- [ ] Tested manually (describe how)
- [ ] No tests (reason): <!-- required if checked -->

<!-- Denote exactly how to run the relevant tests (commands, fixtures). -->

## Screenshots / Videos
<!-- Attach screenshots or recordings if the change is observable; remove the
     placeholder text (not the heading) if not applicable. -->

## Related Issues
<!-- List the related issue / task IDs (REQUEST, URD, PROPOSAL, IMPL_PLAN,
     SLICE, follow-up). If your tracker auto-links (e.g. GitLab `%{issues}` /
     GitHub `Closes #123`), use it here. -->

## Changes
<!-- The key changes made, grouped by area or component. Bullet points; one
     line per meaningful change. -->

## Blast Radius
<!-- Potential side effects: what could this break, what is backwards-compatible,
     what downstream consumers are affected, what is additive vs breaking. -->

## Checklist
- [ ] All relevant tests pass
- [ ] Quality gates pass (typecheck, lint, build)
- [ ] Documentation updated (if applicable)
- [ ] No debug logs, commented-out code, or stray TODOs
- [ ] Follows project coding standards (see CONSTRAINTS.md)

## Reviewer Notes
<!-- Anything reviewers should scrutinise: design decisions worth challenging,
     known limitations, areas requiring attention, open questions. -->
