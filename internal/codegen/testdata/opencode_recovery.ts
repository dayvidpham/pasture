import assert from "node:assert/strict";
const mod = await import(Bun.argv[2]);
const marker = "[pasture:task-recovery]";
const logs: string[] = [];
console.error = (...args) => logs.push(args.join(" "));
const calls: any[] = [];
// The generated plugin samples session.context heartbeats; advance the clock
// so each composition stage still tests a lifecycle consultation.
let clock = 0;
const originalNow = Date.now;
Date.now = () => (clock += 61_000);
Bun.spawn = ((options) => {
  const record = { argv: options.cmd, payload: undefined };
  calls.push(record);
  return { exited: options.stdin.text().then(text => { record.payload = JSON.parse(text); return 0; }), stdout: new Response('{"decision":"proceed"}').body,
    stderr: new Response("").body, kill() {} };
}) as any;
function location(directory: string, fault = false) {
  const hooks: Record<string, Function> = {};
  const disposed: string[] = [];
  const domain = (prefix: string) => ({ hook: async (name, cb) => {
    if (fault && prefix === "session" && name === "context") throw new Error("registration unavailable");
    const key = prefix + "." + name;
    assert.ok(!hooks[key], "one composed callback per coordinate");
    hooks[key] = cb;
    return { dispose: async () => { disposed.push(key); delete hooks[key]; } };
  }});
  return { hooks, disposed, ctx: { app: { version: "2.0.20" }, location: { directory },
    session: domain("session"), tool: domain("tool"), permission: domain("permission"), shell: domain("shell"),
    event: { subscribe: ({ signal }) => (async function* () { if (!signal.aborted) return; })() } } };
}
const a = location("/constructed/a"), b = location("/constructed/b");
const cleanA = await mod.default.setup(a.ctx), cleanB = await mod.default.setup(b.ctx);
const before = { type: "text", text: "unrelated before" }, after = { type: "text", text: "unrelated after" };
// SessionContext/SessionCompaction at OpenCode v2.0.20:
// packages/plugin/src/promise/session.ts (system: Array<SystemPart>).
const event = { sessionID: "constructed", agent: "constructed", model: { providerID: "constructed", id: "constructed" },
  system: [before, after], messages: [], tools: {}, options: {} };
const verify = (e) => {
  assert.equal(e.system.filter(p => p.type === "text" && p.text.startsWith(marker)).length, 1);
  const text = e.system.find(p => p.text.startsWith(marker)).text;
  for (const phrase of ["pasture task ready", "pasture task blocked", "--status in_progress", "--author", "git agent-commit"])
    assert.ok(text.includes(phrase), phrase);
};
for (const stage of ["first", "repeat", "continuation"]) {
  const n = calls.length;
  await a.hooks["session.context"](event);
  verify(event);
  assert.equal(calls.length, n + 1, stage + " retains lifecycle consultation");
  assert.ok(calls.at(-1).argv.includes("session.context"));
  assert.deepEqual(calls.at(-1).payload, { sessionID: "constructed" }, "consult only the session identity, never the assembled context");
  assert.deepEqual(event.system.filter(p => !p.text.startsWith(marker)), [before, after]);
}
const compacted = { ...event, system: [after] };
const nCompaction = calls.length;
await a.hooks["session.compaction"](compacted);
assert.equal(calls.length, nCompaction + 1, "compaction receipt transport remains intact");
assert.deepEqual(compacted.system, [after], "compaction lifecycle callback stays observation-only");
await a.hooks["session.context"](compacted);
verify(compacted);
assert.equal(compacted.system[0], after);
const duplicate = { ...event, system: [before, { type: "text", text: marker + "\nobsolete" }, after,
  { type: "text", text: marker + "\nduplicate" }] };
await a.hooks["session.context"](duplicate);
verify(duplicate);
assert.equal(duplicate.system[0], before);
assert.equal(duplicate.system[2], after);
assert.ok(!duplicate.system[1].text.includes("obsolete"));
const unrelatedMarker = { type: "text", text: "Quoted instruction " + marker + "\nnot owned" };
const quoted = { ...event, system: [unrelatedMarker] };
await a.hooks["session.context"](quoted);
verify(quoted);
assert.equal(quoted.system[0], unrelatedMarker, "marker mentioned inside unrelated text is not owned");
const other = { ...event, sessionID: "other", system: [after] };
await b.hooks["session.context"](other);
verify(other);
assert.notEqual(other.system, event.system);
const n = calls.length;
await a.hooks["session.context"]({ ...event, system: Object.freeze([before]) });
assert.equal(calls.length, n + 1, "injection failure cannot suppress receipt transport");
assert.equal(logs.length, 1);
assert.ok(logs[0].includes("session.context") && logs[0].includes("/constructed/a") && logs[0].includes("2.0.20"));
await a.hooks["session.context"]({ ...event, system: Object.freeze([before]) });
assert.equal(calls.length, n + 2, "persistent injection fault still consults lifecycle");
assert.equal(logs.length, 1, "persistent injection fault reports once per registration");
await cleanA(); await cleanB();
assert.equal(a.disposed.length, 16); assert.equal(b.disposed.length, 16);
assert.equal(Object.keys(a.hooks).length, 0);
assert.equal(Object.keys(b.hooks).length, 0);
const faulty = location("/constructed/fault", true);
const cleanup = await mod.default.setup(faulty.ctx);
assert.equal(logs.length, 2);
assert.ok(logs[1].includes("registration") && logs[1].includes("/constructed/fault"));
await faulty.hooks["session.prompt"]({ sessionID: "constructed" });
await cleanup();
assert.equal(faulty.disposed.length, 15);
Date.now = originalNow;
console.log("recovery composition assertions passed");
