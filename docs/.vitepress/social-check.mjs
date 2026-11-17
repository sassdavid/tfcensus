// Checks the built site's social cards and icons (docs-site.md "Checks", step 4): every page has
// Open Graph and Twitter image tags that point at a 1200 by 630 PNG in dist/social, no card is
// left unused, and the icons and manifest that every page links exist. Run it after vitepress
// build; it prints each problem and exits 1. Shared with the sibling repo.
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { height, pngSize, width } from "./social.mjs";
import { readTokens } from "./tokens.mjs";

const icons = {
  "apple-touch-icon.png": { width: 180, height: 180 },
  "icon-192.png": { width: 192, height: 192 },
  "icon-512.png": { width: 512, height: 512 },
};

// The links every page's head must have, by rel and href.
const headLinks = [
  ["icon", "/favicon.ico"],
  ["icon", "/favicon.svg"],
  ["apple-touch-icon", "/apple-touch-icon.png"],
  ["manifest", "/site.webmanifest"],
];

/** Returns the attributes of every tag with the given name in html. */
export function tags(html, name) {
  const out = [];
  for (const m of html.matchAll(new RegExp(`<${name}\\s([^>]*)>`, "gi"))) {
    const attrs = {};
    for (const a of m[1].matchAll(/([a-zA-Z:-]+)="([^"]*)"/g)) {
      attrs[a[1]] = a[2].replace(/&amp;/g, "&");
    }
    out.push(attrs);
  }
  return out;
}

function htmlFiles(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const path = join(dir, e.name);
    if (e.isDirectory()) {
      return htmlFiles(path);
    }
    return e.name.endsWith(".html") ? [path] : [];
  });
}

/**
 * Returns the problems with one page: missing or mismatched image tags, a card file that is
 * missing or the wrong size, and missing icon links.
 * @param {string} html
 * @param {(path: string) => Buffer | undefined} read reads a file in dist by its site path
 */
export function checkPage(html, read) {
  const problems = [];
  const meta = Object.fromEntries(tags(html, "meta").map((t) => [t.property ?? t.name, t.content]));
  const og = meta["og:image"];
  if (!og) {
    return ["no og:image"];
  }
  if (meta["twitter:image"] !== og) {
    problems.push(`twitter:image ${meta["twitter:image"]} differs from og:image ${og}`);
  }
  if (meta["twitter:card"] !== "summary_large_image") {
    problems.push(`twitter:card is ${meta["twitter:card"]}, want summary_large_image`);
  }
  const canonical = tags(html, "link").find((t) => t.rel === "canonical")?.href;
  const url = URL.canParse(og) ? new URL(og) : undefined;
  if (!url || !/^\/social\/[0-9a-f]{16}\.png$/.test(url.pathname)) {
    problems.push(`og:image ${og} is not an absolute /social/<hash>.png URL`);
  } else {
    if (!canonical || new URL(canonical).origin !== url.origin) {
      problems.push(`og:image ${og} is not on the canonical origin of ${canonical}`);
    }
    const size = pngSize(read(url.pathname) ?? Buffer.alloc(0));
    if (!size) {
      problems.push(`card ${url.pathname} is missing or not a PNG`);
    } else if (size.width !== width || size.height !== height) {
      problems.push(
        `card ${url.pathname} is ${size.width}x${size.height}, want ${width}x${height}`,
      );
    }
  }
  const links = tags(html, "link");
  for (const [rel, href] of headLinks) {
    if (!links.some((l) => l.rel === rel && l.href === href)) {
      problems.push(`no <link rel="${rel}" href="${href}">`);
    }
  }
  return problems;
}

/**
 * Returns the problems with the icons and the manifest.
 * @param {(path: string) => Buffer | undefined} read
 * @param {string} brand the light theme brand colour, the manifest's theme_color
 */
export function checkIcons(read, brand) {
  const problems = [];
  for (const [name, want] of Object.entries(icons)) {
    const size = pngSize(read(`/${name}`) ?? Buffer.alloc(0));
    if (!size || size.width !== want.width || size.height !== want.height) {
      problems.push(
        `${name} is ${size ? `${size.width}x${size.height}` : "missing"}, want ${want.width}x${want.height}`,
      );
    }
  }
  if (!read("/favicon.svg")?.toString().includes("<svg")) {
    problems.push("favicon.svg is missing or not an SVG");
  }
  const favicon = read("/favicon.ico") ?? Buffer.alloc(0);
  const inner =
    favicon.length > 22 ? pngSize(favicon.subarray(favicon.readUInt32LE(18))) : undefined;
  if (favicon.length <= 22 || favicon.readUInt16LE(2) !== 1 || inner?.width !== 32) {
    problems.push("favicon.ico is missing or not a 32x32 icon");
  }
  const raw = read("/site.webmanifest");
  let manifest;
  try {
    manifest = JSON.parse(raw?.toString() ?? "");
  } catch {
    return [...problems, "site.webmanifest is missing or not JSON"];
  }
  if (manifest.theme_color?.toLowerCase() !== brand) {
    problems.push(`site.webmanifest theme_color is ${manifest.theme_color}, want ${brand}`);
  }
  for (const icon of manifest.icons ?? []) {
    if (!read(icon.src)) {
      problems.push(`site.webmanifest icon ${icon.src} is missing`);
    }
  }
  return problems;
}

/** Returns every problem in the built site at dist, each prefixed with its file. */
export function checkSite(dist) {
  const read = (path) => {
    const file = join(dist, decodeURIComponent(path));
    return existsSync(file) ? readFileSync(file) : undefined;
  };
  const problems = [];
  const used = new Set();
  const pages = htmlFiles(dist);
  for (const file of pages) {
    const html = readFileSync(file, "utf8");
    const og = tags(html, "meta").find((t) => t.property === "og:image")?.content;
    if (og && URL.canParse(og)) {
      used.add(new URL(og).pathname);
    }
    for (const p of checkPage(html, read)) {
      problems.push(`${relative(dist, file)}: ${p}`);
    }
  }
  const social = join(dist, "social");
  for (const name of existsSync(social) ? readdirSync(social) : []) {
    if (!used.has(`/social/${name}`)) {
      problems.push(`social/${name}: no page uses this card`);
    }
  }
  const css = readFileSync(new URL("./theme/tokens.css", import.meta.url), "utf8");
  for (const p of checkIcons(read, readTokens(css).light.brand)) {
    problems.push(p);
  }
  if (pages.length === 0) {
    problems.push(`${dist}: no HTML pages, run vitepress build first`);
  }
  return problems;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const dist = process.argv[2] ?? fileURLToPath(new URL("./dist", import.meta.url));
  const problems = checkSite(dist);
  for (const p of problems) {
    console.error(`social-check: ${p}`);
  }
  if (problems.length === 0) {
    console.error(`social-check: ${htmlFiles(dist).length} pages have a card and every icon`);
  }
  process.exitCode = problems.length === 0 ? 0 : 1;
}
