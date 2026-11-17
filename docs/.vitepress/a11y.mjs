// The accessibility check (docs-site.md "Checks", docs-design.md criterion 5). It serves the built
// site, opens each page given on the command line in headless Chrome in the light and the dark
// theme, and runs axe-core through @axe-core/playwright with the WCAG 2.0 to 2.2 A and AA rules
// and the best-practice rules. It also checks that the home page animates only without
// prefers-reduced-motion, and fails on any request to another host. playwright-core downloads no
// browser: Chrome is the one from CHROME_PATH, the first google-chrome, chromium or Chrome.app
// found, or Playwright's "chrome" channel. Shared with the sibling repo.
//   node a11y.mjs / /guides/x /cli/x /reference/config
import { existsSync, readFileSync, statSync } from "node:fs";
import { createServer } from "node:http";
import { delimiter, extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";
import { AxeBuilder } from "@axe-core/playwright";
import { chromium } from "playwright-core";

const tags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa", "best-practice"];
// VitePress renders the doc footer (edit link, previous and next) as a <footer> outside <main>, so
// doc pages have two contentinfo landmarks with the site footer. Lighthouse doesn't count these.
const off = ["landmark-no-duplicate-contentinfo", "landmark-unique"];
const types = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript",
  ".css": "text/css",
  ".json": "application/json",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".woff2": "font/woff2",
  ".woff": "font/woff",
  ".txt": "text/plain; charset=utf-8",
  ".xml": "application/xml",
  ".webmanifest": "application/manifest+json",
};

/** Resolves a request path in dist the way GitHub Pages does with cleanUrls. */
export function resolveFile(dist, path) {
  const clean = normalize(decodeURIComponent(path.split("?")[0])).replace(/^(\.\.[/\\])+/, "");
  const base = join(dist, clean);
  for (const f of [base, `${base}.html`, join(base, "index.html")]) {
    if (existsSync(f) && statSync(f).isFile()) {
      return f;
    }
  }
  return undefined;
}

function serve(dist) {
  const server = createServer((req, res) => {
    const file = resolveFile(dist, req.url ?? "/");
    const status = file ? 200 : 404;
    const body = readFileSync(file ?? join(dist, "404.html"));
    res.writeHead(status, {
      "content-type": types[extname(file ?? ".html")] ?? "application/octet-stream",
    });
    res.end(body);
  });
  return new Promise((resolve) => {
    server.listen(0, "127.0.0.1", () => resolve(server));
  });
}

/** Returns the Chrome binary to use, or undefined. */
export function findChrome(env = process.env) {
  if (env.CHROME_PATH) {
    return env.CHROME_PATH;
  }
  const names = ["google-chrome", "google-chrome-stable", "chromium", "chromium-browser"];
  for (const dir of (env.PATH ?? "").split(delimiter)) {
    for (const name of names) {
      if (dir && existsSync(join(dir, name))) {
        return join(dir, name);
      }
    }
  }
  const mac = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
  return existsSync(mac) ? mac : undefined;
}

/** Returns the requests that leave the site's origin; data: and blob: URLs stay local. */
export function foreign(requests, origin) {
  return requests.filter((r) => !r.startsWith(origin) && !/^(data|blob|about):/.test(r));
}

// open loads url in a new context with the given theme and motion setting, and returns the page
// and the requests it made.
async function open(browser, url, options) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, ...options });
  const page = await context.newPage();
  const requests = [];
  page.on("request", (r) => requests.push(r.url()));
  await page.goto(url, { waitUntil: "networkidle" });
  return { page, requests, close: () => context.close() };
}

async function main(paths) {
  const dist = fileURLToPath(new URL("./dist", import.meta.url));
  if (!existsSync(join(dist, "index.html"))) {
    throw new Error(`${dist} has no index.html: run mise run docs:build first`);
  }
  const chrome = findChrome();
  const server = await serve(dist);
  const origin = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch(
    chrome ? { executablePath: chrome } : { channel: "chrome" },
  );
  const problems = [];
  try {
    for (const path of paths) {
      for (const theme of ["light", "dark"]) {
        const { page, requests, close } = await open(browser, origin + path, {
          colorScheme: theme,
          reducedMotion: "reduce",
        });
        const dark = await page.evaluate(() => document.documentElement.classList.contains("dark"));
        if (dark !== (theme === "dark")) {
          problems.push(`${path} (${theme}): the page did not switch to the ${theme} theme`);
        }
        const { violations } = await new AxeBuilder({ page })
          .withTags(tags)
          .disableRules(off)
          .analyze();
        for (const v of violations) {
          const nodes = v.nodes.slice(0, 5).map((n) => n.target.join(" "));
          problems.push(`${path} (${theme}): ${v.id}: ${v.help}: ${nodes.join(", ")}`);
        }
        for (const r of foreign(requests, origin)) {
          problems.push(`${path} (${theme}): request to another host: ${r}`);
        }
        console.error(`a11y: ${path} (${theme}): ${violations.length} violations`);
        await close();
      }
    }
    const count = async (reducedMotion) => {
      const { page, close } = await open(browser, `${origin}/`, { reducedMotion });
      const n = await page.evaluate(() => document.getAnimations().length);
      await close();
      return n;
    };
    const [still, moving] = [await count("reduce"), await count("no-preference")];
    if (still !== 0) {
      problems.push(`/: ${still} animations run with prefers-reduced-motion: reduce`);
    }
    if (moving === 0) {
      problems.push(
        "/: no animation without prefers-reduced-motion, so the motion check tests nothing",
      );
    }
    console.error(`a11y: home page animations: ${moving} with motion, ${still} reduced`);
  } finally {
    await browser.close();
    server.close();
  }
  return problems;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const paths = process.argv.slice(2);
  if (paths.length === 0) {
    console.error("usage: node a11y.mjs <path>...");
    process.exit(2);
  }
  const problems = await main(paths);
  for (const p of problems) {
    console.error(`a11y: ${p}`);
  }
  process.exitCode = problems.length === 0 ? 0 : 1;
}
