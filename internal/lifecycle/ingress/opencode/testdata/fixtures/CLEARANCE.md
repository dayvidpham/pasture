# Clearance record

This file records how the fixtures in this directory were captured, cleared
and accepted. Fill every section before the acceptance test; leave the last
two sections for the acceptance and the landing. The procedure is documented
in AGENTS.md under "Capturing host payloads and clearing them into fixtures".

## Harness and pinned version

<harness name> <exact host version>, verified with `<host> --version` on
<date>.

## Capture

Captured in live sessions on <dates>, into `<absolute directory outside the
repository>`, with `PASTURE_CAPTURE_DIR` set. One line per event: event,
trigger used, time, payload digest.

## Inventory

Output of the inventory report, per fixture: every field path with its value
class, free-text fields flagged.

## Rules applied, in order

Per fixture, the value-only rules applied in the order applied, as listed in
the provenance sidecar: `home-path-v1`, then `free-text-v1` where the
inventory flagged free text. Structure, keys, types and nulls are unchanged.

## Secret scan

Result of the secret scan over this directory: zero hits, with the test run
that proved it.

## Refused classes

Confirmation that no fixture carries a tool response above 4096 bytes, an
environment dump, or unsubstituted free text on a prompt or message event.
List any payload that was unclearable and the event it leaves withheld.

## Fixtures

One line per committed fixture: file name, native event, payload digest.

## User acceptance

The user's verbatim acceptance for this batch, with its date. Nothing in this
directory reaches a remote before this section is filled. This file is the
clearance authority a fixture's provenance names by path: a fixture may name
this file only after this section holds the acceptance, so that a reader who
follows the path finds the grant recorded and never a blank form.

## Pull request

Appended by the integrator in the landing commit: the pull request URL.
