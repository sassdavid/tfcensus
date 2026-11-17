// Unit tests for social.mjs and social-check.mjs, and the check that the committed PNG icons are
// current.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { createCanvas } from "@napi-rs/canvas";
import { checkIcons, checkPage, tags } from "./social-check.mjs";
import {
  fontFiles,
  height,
  ico,
  icons,
  margin,
  pageLink,
  pngSize,
  renderCard,
  sameImage,
  sectionOf,
  titleMeasure,
  width,
  wrapTitle,
} from "./social.mjs";
import * as design from "./theme/card.mjs";
import { readTokens } from "./tokens.mjs";

const tokens = readTokens(readFileSync(new URL("./theme/tokens.css", import.meta.url), "utf8"));
const glyph = readFileSync(new URL("../public/glyph.svg", import.meta.url));
const charWidth = (text) => text.length * 10;

const png = (w, h) => {
  const b = Buffer.alloc(24);
  Buffer.from("89504e470d0a1a0a", "hex").copy(b);
  b.writeUInt32BE(w, 16);
  b.writeUInt32BE(h, 20);
  return b;
};

test("wrapTitle keeps short titles on one line and wraps long ones at two", () => {
  assert.deepEqual(wrapTitle("a b c", charWidth, 100), ["a b c"]);
  assert.deepEqual(wrapTitle("aaaa bbbb cccc", charWidth, 90), ["aaaa bbbb", "cccc"]);
  assert.deepEqual(wrapTitle("aaaa bbbb cccc dddd eeee", charWidth, 90), ["aaaa bbbb", "cccc…"]);
});

test("the card font and its license come from the site's fontsource package", () => {
  const { font, license } = fontFiles();
  assert.match(font, /@fontsource-variable\/.*\.woff2$/);
  assert.match(readFileSync(license, "utf8"), /SIL Open Font License/);
});

test("the title font loads", () => {
  const measure = titleMeasure();
  assert.ok(measure("mmmmmmmmmm") > 400, "the card font did not load");
});

test("sectionOf finds the sidebar group of a page, nested items included", () => {
  const sidebar = [
    { text: "Guides", items: [{ text: "Scan", link: "/guides/scan" }] },
    {
      text: "CLI",
      items: [
        { link: "/cli/", items: [{ link: "/cli/config/", items: [{ link: "/cli/config/show" }] }] },
      ],
    },
  ];
  assert.equal(sectionOf(sidebar, "/guides/scan"), "Guides");
  assert.equal(sectionOf(sidebar, "/cli/config/show"), "CLI");
  assert.equal(sectionOf(sidebar, "/cli/config"), "CLI");
  assert.equal(sectionOf(sidebar, "/"), undefined);
  assert.equal(sectionOf([{ text: "Ref", link: "/ref/", items: [] }], "/ref/"), "Ref");
});

test("pageLink maps Markdown paths to site links", () => {
  assert.equal(pageLink("index.md"), "/");
  assert.equal(pageLink("cli/index.md"), "/cli/");
  assert.equal(pageLink("guides/scan.md"), "/guides/scan");
});

test("ico wraps one PNG behind a 22 byte header", () => {
  const image = png(32, 32);
  const file = ico(image, 32);
  assert.equal(file.length, 22 + image.length);
  assert.equal(file.readUInt16LE(2), 1);
  assert.equal(file.readUInt16LE(4), 1);
  assert.equal(file.readUInt32LE(18), 22);
  assert.deepEqual(pngSize(file.subarray(22)), { width: 32, height: 32 });
});

const draw = (shade) => {
  const canvas = createCanvas(4, 4);
  const ctx = canvas.getContext("2d");
  ctx.fillStyle = `rgb(${shade}, 0, 0)`;
  ctx.fillRect(0, 0, 4, 4);
  return canvas.encode("png");
};

test("sameImage allows the edge pixel drift between Skia builds", async () => {
  const base = await draw(100);
  assert.ok(await sameImage(base, await draw(101)));
  assert.ok(!(await sameImage(base, await draw(110))));
  assert.ok(await sameImage(ico(base, 4), base));
});

test("pngSize reads the header and rejects other files", () => {
  assert.deepEqual(pngSize(png(1200, 630)), { width: 1200, height: 630 });
  assert.equal(pngSize(Buffer.from("<svg/>")), undefined);
});

test("a card renders at 1200x630 with the title inside its column", async () => {
  const measure = titleMeasure();
  const title = "Speed up scans with the aggregator, then check every region and account twice";
  const { png: card, lines } = await renderCard({
    tool: "tool",
    title,
    section: "Guides",
    glyph,
    tokens: tokens.light,
  });
  assert.equal(lines.length, 2);
  assert.ok(lines[1].endsWith("…"));
  for (const line of lines) {
    assert.ok(measure(line) <= design.titleWidth, `${line} is wider than ${design.titleWidth}`);
  }
  assert.ok(margin + design.titleWidth <= width - margin);
  assert.deepEqual(pngSize(card), { width, height });
});

const page = (og, extra = "") =>
  [
    `<link rel="canonical" href="https://tool.test/guides/scan">`,
    `<meta property="og:image" content="${og}">`,
    `<meta name="twitter:card" content="summary_large_image">`,
    `<meta name="twitter:image" content="${og}">`,
    `<link rel="icon" href="/favicon.ico" sizes="32x32">`,
    `<link rel="icon" href="/favicon.svg" type="image/svg+xml">`,
    `<link rel="apple-touch-icon" href="/apple-touch-icon.png">`,
    `<link rel="manifest" href="/site.webmanifest">`,
    extra,
  ].join("\n");

const readCard = (p) => (p === "/social/0123456789abcdef.png" ? png(1200, 630) : undefined);

test("checkPage accepts a page with a card and every icon link", () => {
  const card = "https://tool.test/social/0123456789abcdef.png";
  assert.deepEqual(checkPage(page(card), readCard), []);
});

test("checkPage reports a missing, small or foreign card", () => {
  assert.deepEqual(
    checkPage("<html></html>", () => undefined),
    ["no og:image"],
  );
  const card = "https://tool.test/social/0123456789abcdef.png";
  assert.match(checkPage(page(card), () => png(600, 315)).join("\n"), /600x315/);
  assert.match(checkPage(page(card), () => undefined).join("\n"), /missing or not a PNG/);
  const foreign = "https://cdn.example.test/social/0123456789abcdef.png";
  assert.match(checkPage(page(foreign), () => png(1200, 630)).join("\n"), /canonical origin/);
  assert.match(checkPage(page("/social/x.png"), () => undefined).join("\n"), /absolute/);
});

test("tags reads attributes and decodes &amp;", () => {
  assert.deepEqual(tags('<meta name="a" content="x &amp; y">', "meta"), [
    { name: "a", content: "x & y" },
  ]);
});

test("checkIcons wants the icon sizes, the SVG favicon and the manifest colour", () => {
  const files = {
    "/favicon.ico": ico(png(32, 32), 32),
    "/apple-touch-icon.png": png(180, 180),
    "/icon-192.png": png(192, 192),
    "/icon-512.png": png(512, 512),
    "/favicon.svg": Buffer.from("<svg/>"),
    "/site.webmanifest": Buffer.from(
      JSON.stringify({ theme_color: "#ABCDEF", icons: [{ src: "/favicon.svg" }] }),
    ),
  };
  assert.deepEqual(
    checkIcons((p) => files[p], "#abcdef"),
    [],
  );
  const small = { ...files, "/favicon.ico": ico(png(16, 16), 16), "/icon-192.png": png(96, 96) };
  const problems = checkIcons((p) => small[p], "#000000");
  assert.match(problems.join("\n"), /favicon.ico is missing or not a 32x32 icon/);
  assert.match(problems.join("\n"), /icon-192.png is 96x96/);
  assert.match(problems.join("\n"), /theme_color/);
});

test("the committed raster icons are current", async () => {
  for (const [name, want] of Object.entries(await icons())) {
    const have = readFileSync(new URL(`../public/${name}`, import.meta.url));
    assert.ok(
      await sameImage(have, want),
      `run node docs/.vitepress/social.mjs to regenerate ${name}`,
    );
  }
});

test("the manifest's theme colour is the light brand token", () => {
  const manifest = JSON.parse(
    readFileSync(new URL("../public/site.webmanifest", import.meta.url), "utf8"),
  );
  assert.equal(manifest.theme_color, tokens.light.brand);
  assert.equal(manifest.background_color, tokens.light.paper);
});
