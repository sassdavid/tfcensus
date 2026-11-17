// Reads the colour tokens of theme/tokens.css and measures contrast. config.mts uses it for code
// comment colours and contrast.test.mjs for the WCAG check. Shared with the sibling repo.

/**
 * Returns the tool prefix and the light (:root) and dark (.dark) values of every `--<prefix>-*`
 * token, keyed by the name after the prefix.
 * @param {string} css
 * @returns {{ prefix: string, light: Record<string, string>, dark: Record<string, string> }}
 */
export function readTokens(css) {
  const paper = /--([a-z]+)-paper:/.exec(css);
  if (!paper) {
    throw new Error("tokens.css: no --<prefix>-paper token");
  }
  const prefix = paper[1];
  return { prefix, light: block(css, ":root", prefix), dark: block(css, ".dark", prefix) };
}

function block(css, selector, prefix) {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = new RegExp(`(?:^|\\n)${escaped}\\s*\\{([^}]*)\\}`).exec(css);
  if (!match) {
    throw new Error(`tokens.css: no ${selector} block`);
  }
  const tokens = {};
  for (const m of match[1].matchAll(
    new RegExp(`--${prefix}-([a-z-]+):\\s*(#[0-9a-fA-F]{6})\\s*;`, "g"),
  )) {
    tokens[m[1]] = m[2].toLowerCase();
  }
  return tokens;
}

/**
 * WCAG 2.x contrast ratio of two #rrggbb colours.
 * @param {string} a
 * @param {string} b
 */
export function contrast(a, b) {
  const [hi, lo] = [luminance(a), luminance(b)].toSorted((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

function luminance(hex) {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = Number.parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}
