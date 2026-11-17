// The contrast check (docs-site.md "Checks"): text tokens need 4.5:1 and the control token 3:1
// against paper and surface in both themes. The rule token is decorative and isn't checked.
// The primary button puts paper on a brand fill, so that pair needs 4.5:1 as well.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { contrast, readTokens } from "./tokens.mjs";

const css = readFileSync(new URL("./theme/tokens.css", import.meta.url), "utf8");
const tokens = readTokens(css);
const surfaces = ["paper", "surface"];
const decorative = ["rule"];
const graphic = { control: 3 };

test("both themes define the same tokens", () => {
  assert.deepEqual(Object.keys(tokens.dark).sort(), Object.keys(tokens.light).sort());
  assert.ok(Object.keys(tokens.light).length >= surfaces.length + 3);
});

for (const theme of ["light", "dark"]) {
  const t = tokens[theme];
  for (const [name, colour] of Object.entries(t)) {
    if (surfaces.includes(name) || decorative.includes(name)) {
      continue;
    }
    const floor = graphic[name] ?? 4.5;
    for (const surface of surfaces) {
      test(`${theme}: ${name} on ${surface} is at least ${floor}:1`, () => {
        const ratio = contrast(colour, t[surface]);
        assert.ok(ratio >= floor, `${colour} on ${t[surface]} is ${ratio.toFixed(2)}:1`);
      });
    }
  }
  test(`${theme}: paper on brand is at least 4.5:1`, () => {
    const ratio = contrast(t.paper, t.brand);
    assert.ok(ratio >= 4.5, `${t.paper} on ${t.brand} is ${ratio.toFixed(2)}:1`);
  });
}

test("contrast matches the WCAG reference values", () => {
  assert.equal(contrast("#000000", "#ffffff").toFixed(2), "21.00");
  assert.equal(contrast("#777777", "#ffffff").toFixed(2), "4.48");
});

test("readTokens finds the prefix and both blocks", () => {
  const got = readTokens(
    ":root {\n  --xy-paper: #FFFFFF;\n  --xy-ink: #000000;\n}\n.dark {\n  --xy-paper: #000000;\n}\n",
  );
  assert.deepEqual(got, {
    prefix: "xy",
    light: { paper: "#ffffff", ink: "#000000" },
    dark: { paper: "#000000" },
  });
});
