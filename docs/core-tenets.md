# Core tenets

Design rules for the codebase. Short by intent. Read this before you change a
schema, a generator, or a guard.

## Terms

- **Binding**: the table that maps one operation to each harness's tool.
- **Lowering**: how one operation becomes a real tool call on one harness.
- **Closed set**: a fixed list. To add a value, register it.
- **Neutral name**: the operation name used in prose, before a harness is chosen.
- **Guard**: a test that fails when a specific defect returns.

## The tenets

**1. Generate, do not hand-edit.**
One schema is the source. All harness text is generated. Never edit generated files.

**2. Keep sets closed.**
Every label, operation and enum is a registered list. To add a value, register it.
Do not invent a value at a use site.

**3. Write the operation, not the tool.**
Prose names the operation. One binding table maps it to each harness.

**4. Name it once.**
A tool name, command string or identifier appears in one place. Everywhere else
uses the constant.

**5. Type the closed set.**
If a field holds a fixed set of values, give it a type. Do not store it as a
string.

**6. Fail the build, not the run.**
The build fails when a rule has no rendering, a label is unregistered, or a
retired name appears.

**7. Give each fact one home.**
Severity lives on the group. The finding carries one label. One registry, one
lowering map, one store.

**8. Prove each guard.**
A guard must fail on a known defect. A guard that cannot fail is not a guard.

**9. Test a guard with two samples.**
One defective sample must produce at least one finding. One clean sample must
produce none. Do not pin counts. Counts change with every wording edit.

**10. Reset on a repeat.**
If the same finding returns in two rounds, the model is wrong. Re-derive from the
requirements. Do not patch.

**11. Delete the contradiction.**
When a rule changes, remove the old rule in the same change. Do not leave both.

**12. Record once, derive later.**
Write the record when it happens. Build views from the record.

## What each tenet prevents

| Tenet | Prevents |
|---|---|
| 1 | Three hand-maintained trees that drift apart |
| 2 | Labels and operations invented at a use site |
| 3 | A tool name from one harness shipping into another |
| 4 | The same command string copied into thirty files |
| 5 | A field that accepts any string and therefore any defect |
| 6 | A defect found one build late, or by a reader |
| 7 | Severity recorded in two places that disagree |
| 8 | Guards that pass because they assert nothing |
| 9 | Guards that break on every wording change |
| 10 | Twenty-one proposals that patch the same flaw |
| 11 | Two rules that contradict each other, both live |
| 12 | Records written after the fact, from memory |

## Applying a tenet

When you add a capability, ask in order:

1. Is this a new value in a closed set? Register it.
2. Is this harness-specific? Add a lowering, not a sentence.
3. Does this string already exist? Use the constant.
4. Does a field hold a fixed set? Give it a type.
5. Does the build catch this? If not, add a guard.
6. Can the guard fail? Write the defective sample first.
