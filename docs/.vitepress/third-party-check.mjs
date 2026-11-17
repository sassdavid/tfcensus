// Checks that the built site loads nothing from another host (docs-site.md acceptance criterion
// 10): no external src, no stylesheet, script, font, icon or manifest href, no CSS url() or
// @import, and no fetch, import(), WebSocket, EventSource or beacon to another origin in the JS.
// Links a reader follows (<a href>) are not requests and stay allowed. Run it after vitepress
// build; it prints each problem and exits 1. Shared with the sibling repo.
import { readdirSync, readFileSync } from "node:fs";
import { extname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { tags } from "./social-check.mjs";

// Link relations that make the browser fetch the target.
const fetchingRels = new Set([
  "stylesheet",
  "preload",
  "modulepreload",
  "prefetch",
  "preconnect",
  "dns-prefetch",
  "icon",
  "apple-touch-icon",
  "mask-icon",
  "manifest",
]);

const srcTags = ["script", "img", "iframe", "source", "video", "audio", "embed", "track", "input"];

/** Reports whether url points at a host other than the site's own. */
export function offsite(url) {
  return /^(https?:|wss?:)?\/\//i.test(url.trim());
}

/** Returns the requests to another host that an HTML page makes. */
export function htmlProblems(html) {
  const problems = [];
  for (const name of srcTags) {
    for (const t of tags(html, name)) {
      for (const attr of ["src", "srcset", "poster", "data"]) {
        if (t[attr] && t[attr].split(",").some((u) => offsite(u))) {
          problems.push(`<${name} ${attr}="${t[attr]}">`);
        }
      }
    }
  }
  for (const t of tags(html, "link")) {
    const rels = (t.rel ?? "").toLowerCase().split(/\s+/);
    if (t.href && offsite(t.href) && rels.some((r) => fetchingRels.has(r))) {
      problems.push(`<link rel="${t.rel}" href="${t.href}">`);
    }
  }
  for (const m of html.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/gi)) {
    problems.push(...cssProblems(m[1]));
  }
  return problems;
}

/** Returns the external url() and @import references in a stylesheet. */
export function cssProblems(css) {
  const problems = [];
  for (const m of css.matchAll(/url\(\s*["']?([^"')]+)["']?\s*\)/gi)) {
    if (offsite(m[1])) {
      problems.push(`url(${m[1]})`);
    }
  }
  for (const m of css.matchAll(/@import\s+(?:url\()?\s*["']([^"']+)["']/gi)) {
    if (offsite(m[1])) {
      problems.push(`@import ${m[1]}`);
    }
  }
  return problems;
}

/** Returns the calls in a script that would fetch from another host. */
export function jsProblems(js) {
  const problems = [];
  const call =
    /\b(fetch|import|sendBeacon|WebSocket|EventSource|importScripts)\s*\(\s*(["'`])((?:https?:|wss?:)?\/\/[^"'`]+)\2/g;
  for (const m of js.matchAll(call)) {
    problems.push(`${m[1]}("${m[3]}")`);
  }
  return problems;
}

function files(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const path = join(dir, e.name);
    return e.isDirectory() ? files(path) : [path];
  });
}

/** Returns every request to another host in the built site at dist, prefixed with its file. */
export function checkSite(dist) {
  const problems = [];
  const check = {
    ".html": htmlProblems,
    ".css": cssProblems,
    ".js": jsProblems,
    ".mjs": jsProblems,
  };
  for (const file of files(dist)) {
    const fn = check[extname(file)];
    if (fn) {
      for (const p of fn(readFileSync(file, "utf8"))) {
        problems.push(`${relative(dist, file)}: ${p}`);
      }
    }
  }
  return problems;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const dist = process.argv[2] ?? fileURLToPath(new URL("./dist", import.meta.url));
  const problems = checkSite(dist);
  for (const p of problems) {
    console.error(`third-party-check: ${p}`);
  }
  if (problems.length === 0) {
    console.error(`third-party-check: ${files(dist).length} files load nothing from another host`);
  }
  process.exitCode = problems.length === 0 ? 0 : 1;
}
