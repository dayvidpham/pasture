
import assert from "node:assert/strict";
import { Writable } from "node:stream";
const {default: plugin} = await import(Bun.argv[2]);
const hooks = {};
const ctx = {
  app: { name: "opencode", version: "2.0.20" },
  session: { hook: async (name, cb) => { hooks["session." + name] = cb; return { dispose: async () => {} }; } },
  tool: { hook: async (name, cb) => { hooks["tool." + name] = cb; return { dispose: async () => {} }; } },
  permission: { hook: async (name, cb) => { hooks["permission." + name] = cb; return { dispose: async () => {} }; } },
  shell: { hook: async (name, cb) => { hooks["shell." + name] = cb; return { dispose: async () => {} }; } },
  event: { subscribe: () => (async function* () {})() },
};
const originalSpawn = Bun.spawn;
const originalStderr = process.stderr;
const originalConsoleError = console.error;
let childExit = 0;
let childStdout = '{"decision":"deny","reason":"guarded"}';
let forwarded = "";
Bun.spawn = () => ({
  stdout: new Blob([childStdout]).stream(),
  stderr: new Blob(["child diagnostic"]).stream(),
  exited: Promise.resolve(childExit),
  exitCode: childExit,
  kill() { throw new Error("unexpected kill"); },
});
const installSink = sink => Object.defineProperty(process, "stderr", {value: sink, configurable: true});
try {
  await plugin.setup(ctx);
  const payload = { permission: "tool", action: "task" };
  for (const mode of ["destroyed", "epipe"]) {
    const sink = new Writable({ write(_chunk, _encoding, callback) {
      callback(mode === "epipe" ? Object.assign(new Error("simulated EPIPE"), {code: "EPIPE"}) : undefined);
    }});
    if (mode === "destroyed") sink.destroy();
    installSink(sink);
    const event = { ...payload };
    await hooks["permission.evaluate"](event);
    assert.equal(event.effect, "deny", mode + " sink must not discard the child's deny decision");
    assert.equal(event.message, "guarded", mode + " sink must preserve the exact decision reason");
  }
  const healthy = new Writable({ write(chunk, _encoding, callback) { forwarded += chunk.toString(); callback(); }});
  installSink(healthy);
  const proceed = { ...payload };
  childStdout = '{"decision":"proceed"}';
  await hooks["permission.evaluate"](proceed);
  assert.equal(forwarded, "child diagnostic\n", "healthy sink receives the exact diagnostic and newline policy");
  assert.equal(proceed.effect, undefined, "proceed remains unmutated");
  const errors = [];
  console.error = (...args) => errors.push(args.join(" "));
  childExit = 7;
  const failedChild = { ...payload };
  await hooks["permission.evaluate"](failedChild);
  assert.equal(failedChild.effect, undefined, "nonzero child exit cannot become a decision");
  assert.match(errors.join("\n"), /exited 7: child diagnostic/, "nonzero child failure remains fatal to the consultation");
  console.log("diagnostic sink assertions passed");
} finally {
  Bun.spawn = originalSpawn;
  Object.defineProperty(process, "stderr", {value: originalStderr, configurable: true});
  console.error = originalConsoleError;
}
