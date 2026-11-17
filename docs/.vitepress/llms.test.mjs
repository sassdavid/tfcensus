// Unit tests for llms.mts, and the check that the committed llms.txt is current.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { buildLlms, frontmatter, pageFiles, siteLlms } from "./llms.mts";

test("frontmatter reads quoted and plain values", () => {
  const fm = frontmatter('---\n# comment\ntitle: "a \\"b\\""\ndescription: plain text\n---\n# x\n');
  assert.deepEqual(fm, { title: 'a "b"', description: "plain text" });
  assert.deepEqual(frontmatter("# no frontmatter\n"), {});
});

test("pageFiles maps links to Markdown files", () => {
  assert.deepEqual(pageFiles("/cli/"), ["cli/index.md", "cli/README.md"]);
  assert.deepEqual(pageFiles("/cli/scan"), ["cli/scan.md", "cli/scan/index.md"]);
  assert.deepEqual(pageFiles("/"), ["index.md", "README.md"]);
});

test("buildLlms writes one section per group with nested pages", () => {
  const text = buildLlms({
    site: "https://example.test",
    title: "tool",
    summary: "What it does.",
    groups: [
      { text: "Start", items: [{ text: "Install", link: "/install" }] },
      {
        text: "CLI",
        link: "/cli/",
        items: [{ text: "a", link: "/cli/a/", items: [{ text: "b", link: "/cli/a/b" }] }],
      },
    ],
    page: (link) =>
      link === "/install" ? { title: "Install tool", description: "Get it." } : undefined,
  });
  assert.equal(
    text,
    [
      "# tool",
      "",
      "> What it does.",
      "",
      "## Start",
      "",
      "- [Install tool](https://example.test/install): Get it.",
      "",
      "## CLI",
      "",
      "- [CLI](https://example.test/cli/)",
      "- [a](https://example.test/cli/a/)",
      "- [b](https://example.test/cli/a/b)",
      "",
    ].join("\n"),
  );
});

test("the committed llms.txt is current", () => {
  const have = readFileSync(new URL("../public/llms.txt", import.meta.url), "utf8");
  assert.equal(have, siteLlms(), "run mise run docs:build to regenerate docs/public/llms.txt");
});
