// Unit tests for third-party-check.mjs and the helpers in a11y.mjs.
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { findChrome, foreign, resolveFile } from "./a11y.mjs";
import { cssProblems, htmlProblems, jsProblems, offsite } from "./third-party-check.mjs";

test("offsite tells other hosts from site paths", () => {
  for (const url of [
    "https://cdn.test/a.js",
    "//cdn.test/a.js",
    "wss://x.test",
    " http://x.test",
  ]) {
    assert.ok(offsite(url), url);
  }
  for (const url of ["/assets/a.js", "assets/a.js", "data:image/png;base64,AA", "#top"]) {
    assert.ok(!offsite(url), url);
  }
});

test("htmlProblems reports external resources and allows links", () => {
  const html = [
    '<script src="https://cdn.test/x.js"></script>',
    '<link rel="stylesheet" href="https://fonts.test/a.css">',
    '<link rel="preconnect" href="https://fonts.test">',
    '<img src="//img.test/a.png">',
    "<style>body { background: url(https://img.test/b.png) }</style>",
    '<a href="https://github.com/x">source</a>',
    '<link rel="canonical" href="https://site.test/x">',
    '<script src="/assets/app.js"></script>',
  ].join("\n");
  assert.deepEqual(htmlProblems(html), [
    '<script src="https://cdn.test/x.js">',
    '<img src="//img.test/a.png">',
    '<link rel="stylesheet" href="https://fonts.test/a.css">',
    '<link rel="preconnect" href="https://fonts.test">',
    "url(https://img.test/b.png)",
  ]);
});

test("cssProblems reports external url() and @import", () => {
  const css = `@import "https://fonts.test/a.css"; @font-face { src: url("/f.woff2") } a { background: url('//x.test/i.png') }`;
  assert.deepEqual(cssProblems(css), ["url(//x.test/i.png)", "@import https://fonts.test/a.css"]);
});

test("jsProblems reports fetches to other hosts only", () => {
  const js = `fetch("https://api.test/x");fetch(base+"hashmap.json");import("//cdn.test/m.js");new WebSocket('wss://x.test');const u="https://github.com"`;
  assert.deepEqual(jsProblems(js), [
    'fetch("https://api.test/x")',
    'import("//cdn.test/m.js")',
    'WebSocket("wss://x.test")',
  ]);
});

// tempDir makes a directory that is removed when the test ends.
function tempDir(t, prefix) {
  const dir = mkdtempSync(join(tmpdir(), prefix));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test("resolveFile serves clean URLs like GitHub Pages", (t) => {
  const dist = tempDir(t, "a11y-dist-");
  mkdirSync(join(dist, "cli"));
  for (const f of ["index.html", "guide.html", "cli/index.html", "a.css"]) {
    writeFileSync(join(dist, f), "x");
  }
  assert.equal(resolveFile(dist, "/"), join(dist, "index.html"));
  assert.equal(resolveFile(dist, "/guide"), join(dist, "guide.html"));
  assert.equal(resolveFile(dist, "/cli/"), join(dist, "cli/index.html"));
  assert.equal(resolveFile(dist, "/a.css?v=1"), join(dist, "a.css"));
  assert.equal(resolveFile(dist, "/missing"), undefined);
  assert.equal(resolveFile(dist, "/../../etc/passwd"), undefined);
});

test("foreign keeps requests that leave the origin", () => {
  const origin = "http://127.0.0.1:4000";
  const got = foreign(
    [`${origin}/`, `${origin}/assets/a.js`, "data:font/woff2;base64,AA", "https://cdn.test/x.js"],
    origin,
  );
  assert.deepEqual(got, ["https://cdn.test/x.js"]);
});

test("findChrome prefers CHROME_PATH, then PATH", (t) => {
  assert.equal(findChrome({ CHROME_PATH: "/opt/chrome" }), "/opt/chrome");
  const dir = tempDir(t, "a11y-bin-");
  writeFileSync(join(dir, "chromium"), "");
  assert.equal(findChrome({ PATH: dir }), join(dir, "chromium"));
});
