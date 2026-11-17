// Social cards and raster icons for the docs site (docs-site.md "Checks", docs-design.md "Social
// cards"). config.mts calls socialCards(): transformHead renders each page's card and gets its
// meta tags, and buildEnd writes the cards to dist/social/<hash>.png. `node social.mjs` renders
// favicon.ico and the PNG icons in public/ from glyph.svg and exits 1 when a committed one was
// stale. Drawing uses @napi-rs/canvas (Skia) with the site's own fontsource variable font, so the
// card weights match the site. The layout is shared; the tool's font and artwork come from
// theme/card.mjs. Shared with the sibling repo.
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { createCanvas, GlobalFonts, loadImage } from "@napi-rs/canvas";
import * as design from "./theme/card.mjs";
import { readTokens } from "./tokens.mjs";

export const width = 1200;
export const height = 630;
export const margin = 80;
const titleSize = 64;
const titleLeading = 76;
const sectionSize = 32;

// The font is registered under a name no OS installs, so no system font can stand in for it.
const family = "SocialCardFont";
const here = new URL(".", import.meta.url);
const require = createRequire(import.meta.url);

/** Returns the paths of the card font and of its license file, from the fontsource package. */
export function fontFiles() {
  return { font: require.resolve(design.font.file), license: require.resolve(design.font.license) };
}

let registered = false;
function registerFont() {
  if (!registered) {
    if (!GlobalFonts.registerFromPath(fontFiles().font, family)) {
      throw new Error(`social: can't load ${design.font.file}`);
    }
    registered = true;
  }
}

/**
 * Splits title into at most maxLines lines no wider than maxWidth, word by word. A title that
 * doesn't fit ends its last line with an ellipsis.
 * @param {string} title
 * @param {(text: string) => number} measure width of a line in pixels
 * @param {number} maxWidth
 * @param {number} [maxLines]
 */
export function wrapTitle(title, measure, maxWidth, maxLines = 2) {
  const words = title.split(/\s+/).filter(Boolean);
  const lines = [];
  let i = 0;
  while (i < words.length && lines.length < maxLines) {
    let line = words[i++];
    while (i < words.length && measure(`${line} ${words[i]}`) <= maxWidth) {
      line += ` ${words[i++]}`;
    }
    lines.push(line);
  }
  if (i < words.length) {
    let last = `${lines.pop()} ${words.slice(i).join(" ")}`;
    while (last.includes(" ") && measure(`${last}…`) > maxWidth) {
      last = last.slice(0, last.lastIndexOf(" "));
    }
    lines.push(`${last}…`);
  }
  return lines;
}

/** Returns a function that measures a title line in the card's title font. */
export function titleMeasure() {
  registerFont();
  const ctx = createCanvas(1, 1).getContext("2d");
  ctx.font = `${design.font.title} ${titleSize}px ${family}`;
  return (text) => ctx.measureText(text).width;
}

/**
 * Returns the sidebar group that is or links to link, searching nested items.
 * @param {{ text?: string, link?: string, items?: unknown[] }[]} sidebar
 * @param {string} link
 */
export function sectionOf(sidebar, link) {
  const has = (items) =>
    (items ?? []).some((i) => i.link === link || i.link === `${link}/` || has(i.items));
  return sidebar.find((group) => has([group]))?.text;
}

/** Returns the site link of a page from its path relative to docs/. */
export function pageLink(relativePath) {
  return "/" + relativePath.replace(/(^|\/)index\.md$/, "$1").replace(/\.md$/, "");
}

/** Returns the width and height in a PNG header, or undefined for anything else. */
export function pngSize(buf) {
  const signature = "89504e470d0a1a0a";
  if (buf.length < 24 || buf.subarray(0, 8).toString("hex") !== signature) {
    return undefined;
  }
  return { width: buf.readUInt32BE(16), height: buf.readUInt32BE(20) };
}

/**
 * Renders one card: paper background, glyph and wordmark top left, the title in up to two lines,
 * the section under it, and the tool's artwork from theme/card.mjs. Returns the PNG and the
 * title lines.
 * @param {{ tool: string, title: string, section: string, glyph: Buffer,
 *   tokens: Record<string, string> }} opts
 */
export async function renderCard({ tool, title, section, glyph, tokens }) {
  registerFont();
  const lines = wrapTitle(title, titleMeasure(), design.titleWidth);
  const canvas = createCanvas(width, height);
  const ctx = canvas.getContext("2d");
  ctx.fillStyle = tokens.paper;
  ctx.fillRect(0, 0, width, height);
  ctx.drawImage(await loadImage(glyph), margin, 72, 64, 64);
  ctx.fillStyle = tokens.ink;
  ctx.font = `${design.font.wordmark} 48px ${family}`;
  ctx.fillText(tool, margin + 84, 121);
  ctx.font = `${design.font.title} ${titleSize}px ${family}`;
  const top = 290;
  lines.forEach((line, n) => ctx.fillText(line, margin, top + n * titleLeading));
  if (section) {
    ctx.fillStyle = tokens["ink-muted"];
    ctx.font = `${design.font.title} ${sectionSize}px ${family}`;
    ctx.fillText(section, margin, top + (lines.length - 1) * titleLeading + 60);
  }
  design.art(ctx, tokens, { width, height, margin });
  return { png: await canvas.encode("png"), lines };
}

function hashOf(buf) {
  return createHash("sha256").update(buf).digest("hex").slice(0, 16);
}

/**
 * The VitePress side of the cards. meta() runs in transformHead, renders the page's card and
 * returns its tags; write() runs in buildEnd and writes the cards. A page outside the sidebar,
 * such as the home page, shows the site's host as its section.
 * @param {{ site: string, tool: string, sidebar: { text?: string, items?: unknown[] }[] }} opts
 */
export function socialCards({ site, tool, sidebar }) {
  const glyph = readFileSync(new URL("../public/glyph.svg", here));
  const all = readTokens(readFileSync(new URL("theme/tokens.css", here), "utf8"));
  const tokens = all.light;
  const cards = new Map();

  return {
    /** The icon and manifest links and the theme colours for every page. */
    head() {
      return [
        ["link", { rel: "icon", href: "/favicon.ico", sizes: "32x32" }],
        ["link", { rel: "icon", href: "/favicon.svg", type: "image/svg+xml" }],
        ["link", { rel: "apple-touch-icon", href: "/apple-touch-icon.png" }],
        ["link", { rel: "manifest", href: "/site.webmanifest" }],
        [
          "meta",
          { name: "theme-color", content: all.light.paper, media: "(prefers-color-scheme: light)" },
        ],
        [
          "meta",
          { name: "theme-color", content: all.dark.paper, media: "(prefers-color-scheme: dark)" },
        ],
      ];
    },
    /** Renders the page's card and returns its Open Graph and Twitter image tags. */
    async meta(pageData) {
      const title = String(pageData.frontmatter.headline ?? pageData.title ?? tool);
      const section = sectionOf(sidebar, pageLink(pageData.relativePath)) ?? new URL(site).host;
      const { png } = await renderCard({ tool, title, section, glyph, tokens });
      const file = `social/${hashOf(png)}.png`;
      cards.set(file, png);
      const url = `${site}/${file}`;
      return [
        ["meta", { property: "og:image", content: url }],
        ["meta", { property: "og:image:width", content: String(width) }],
        ["meta", { property: "og:image:height", content: String(height) }],
        ["meta", { property: "og:image:type", content: "image/png" }],
        ["meta", { property: "og:image:alt", content: title }],
        ["meta", { name: "twitter:card", content: "summary_large_image" }],
        ["meta", { name: "twitter:image", content: url }],
        ["meta", { name: "twitter:image:alt", content: title }],
      ];
    },
    /** Writes every card that meta() rendered into outDir. */
    write(outDir) {
      mkdirSync(join(outDir, "social"), { recursive: true });
      for (const [file, png] of cards) {
        writeFileSync(join(outDir, file), png);
      }
    },
  };
}

/** Wraps one PNG in an ICO file, which browsers and crawlers ask for at /favicon.ico. */
export function ico(png, size) {
  const header = Buffer.alloc(22);
  header.writeUInt16LE(1, 2); // type: icon
  header.writeUInt16LE(1, 4); // one image
  header.writeUInt8(size, 6);
  header.writeUInt8(size, 7);
  header.writeUInt16LE(1, 10); // colour planes
  header.writeUInt16LE(32, 12); // bits per pixel
  header.writeUInt32LE(png.length, 14);
  header.writeUInt32LE(22, 18); // the image starts after the header
  return Buffer.concat([header, png]);
}

/**
 * Returns the raster icons the glyph is drawn into: favicon.ico at 32 px, and the touch and
 * manifest icons with the glyph on a paper background, because iOS fills a transparent touch
 * icon with black.
 */
export async function icons() {
  const glyph = await loadImage(readFileSync(new URL("../public/glyph.svg", here)));
  const paper = readTokens(readFileSync(new URL("theme/tokens.css", here), "utf8")).light.paper;
  const favicon = createCanvas(32, 32);
  favicon.getContext("2d").drawImage(glyph, 0, 0, 32, 32);
  const onPaper = async (size) => {
    const canvas = createCanvas(size, size);
    const ctx = canvas.getContext("2d");
    ctx.fillStyle = paper;
    ctx.fillRect(0, 0, size, size);
    ctx.drawImage(glyph, size / 6, size / 6, (size * 2) / 3, (size * 2) / 3);
    return canvas.encode("png");
  };
  return {
    "favicon.ico": ico(await favicon.encode("png"), 32),
    "apple-touch-icon.png": await onPaper(180),
    "icon-192.png": await onPaper(192),
    "icon-512.png": await onPaper(512),
  };
}

/** Returns the RGBA pixels of a PNG, or of the PNG inside a one-image ICO. */
async function pixels(file) {
  const png = file.readUInt16LE(2) === 1 && pngSize(file) === undefined ? file.subarray(22) : file;
  const image = await loadImage(png);
  const canvas = createCanvas(image.width, image.height);
  const ctx = canvas.getContext("2d");
  ctx.drawImage(image, 0, 0);
  return ctx.getImageData(0, 0, image.width, image.height).data;
}

/**
 * Reports whether two raster icons show the same image. Skia's x64 and arm64 builds can differ by
 * a colour level on edge pixels (the @napi-rs/canvas README), so the bytes can't be compared and
 * each channel may differ by up to tolerance.
 */
export async function sameImage(a, b, tolerance = 2) {
  const [pa, pb] = await Promise.all([pixels(a), pixels(b)]);
  return pa.length === pb.length && pa.every((v, i) => Math.abs(v - pb[i]) <= tolerance);
}

// Writes the raster icons to public/ and exits 1 when a committed one shows a different image.
async function main() {
  let stale = false;
  for (const [name, image] of Object.entries(await icons())) {
    const url = new URL(`../public/${name}`, here);
    if (!existsSync(url) || !(await sameImage(readFileSync(url), image))) {
      writeFileSync(url, image);
      console.error(`social: wrote docs/public/${name}`);
      stale = true;
    }
  }
  process.exitCode = stale ? 1 : 0;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  await main();
}
