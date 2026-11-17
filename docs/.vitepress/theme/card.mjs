// The tfcensus part of the social cards (docs-design.md "Social cards"): the site's Public Sans
// variable font and one tally row along the bottom edge, one mark per resource of the home page
// sample. social.mjs draws the rest of the card.
import { readFileSync } from "node:fs";

// The fontsource files the site serves, at the weights docs-design.md gives.
export const font = {
  file: "@fontsource-variable/public-sans/files/public-sans-latin-wght-normal.woff2",
  license: "@fontsource-variable/public-sans/LICENSE",
  title: 650,
  wordmark: 750,
};

// The title spans the card between the margins.
export const titleWidth = 1040;

const tally = JSON.parse(readFileSync(new URL("./data/home-tally.json", import.meta.url), "utf8"));

// mark draws one category mark (CategoryMark.vue, a 16 unit box) at x, y in a size box.
function mark(ctx, tokens, category, x, y, size) {
  const s = size / 16;
  ctx.fillStyle = tokens[category];
  ctx.strokeStyle = tokens[category];
  ctx.lineWidth = 2 * s;
  ctx.setLineDash([]);
  switch (category) {
    case "managed":
      ctx.fillRect(x + s, y + s, 14 * s, 14 * s);
      break;
    case "aws-managed":
      ctx.fillRect(x + s, y + s, 14 * s, 14 * s);
      ctx.globalAlpha = 0.6;
      ctx.fillStyle = tokens.surface;
      ctx.fillRect(x + 5 * s, y + 5 * s, 6 * s, 6 * s);
      ctx.globalAlpha = 1;
      break;
    case "other-iac":
      ctx.strokeRect(x + 2 * s, y + 2 * s, 12 * s, 12 * s);
      break;
    case "derived":
      ctx.fillRect(x + 3.5 * s, y + 3.5 * s, 9 * s, 9 * s);
      break;
    case "unmanaged":
      ctx.beginPath();
      ctx.moveTo(x + s, y + s);
      ctx.lineTo(x + 15 * s, y + s);
      ctx.lineTo(x + 15 * s, y + 9 * s);
      ctx.lineTo(x + 9 * s, y + 15 * s);
      ctx.lineTo(x + s, y + 15 * s);
      ctx.closePath();
      ctx.fill();
      break;
    default:
      ctx.setLineDash([3 * s, 2 * s]);
      ctx.strokeRect(x + 2 * s, y + 2 * s, 12 * s, 12 * s);
      ctx.setLineDash([]);
  }
}

/**
 * Draws the tally row: the home page sample's marks in category order along the bottom edge.
 * @param {import("@napi-rs/canvas").SKRSContext2D} ctx
 * @param {Record<string, string>} tokens light theme colours
 * @param {{ width: number, height: number, margin: number }} card
 */
export function art(ctx, tokens, card) {
  const size = 40;
  const gap = 12;
  const marks = tally.rows.flatMap((r) => Array(Math.ceil(r.count / tally.unit)).fill(r.category));
  const fit = Math.floor((card.width - 2 * card.margin + gap) / (size + gap));
  const y = card.height - card.margin - size;
  marks
    .slice(0, fit)
    .forEach((c, i) => mark(ctx, tokens, c, card.margin + i * (size + gap), y, size));
}
