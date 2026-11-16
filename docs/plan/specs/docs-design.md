# Spec: docs site design (tfcensus)

The visual identity of `https://tfcensus.sasskovacs.dev`. relgap has its own identity
(release strata) and shares only the build and the checks ([docs-site](docs-site.md)). Chosen by
the owner on 2026-11-17: own identity per tool, the "isotype census" concept for tfcensus, and a
wordmark with a small glyph.

## Brief

- Subject: tfcensus counts every resource in an AWS account and sorts it into managed, unmanaged,
  undetermined, aws-managed, other-iac and derived. It also audits state backends, reports drift
  and moves resources between units.
- Audience: platform and DevOps engineers who run Terraform or OpenTofu with Terragrunt across
  many accounts. They read reference pages more than prose, copy commands, and scan tables.
- Primary job of the site: get an engineer from the home page to a first read-only scan, then
  answer "what does this flag, key, category or exit code mean" in one search.

## Concept: isotype census

Census offices have shown counts with isotype since Otto Neurath's Vienna method in the 1920s:
rows of identical unit symbols, one symbol per counted thing, sorted into groups, with a label
and a number. tfcensus produces exactly that kind of count. The site uses the same visual
language:

- One square per resource, coloured by scan category, in rows with a label and a count.
- The same category squares mark categories everywhere: in prose, in tables and in example output.
- Flat colour, no gradients, no shadows, no illustrations. The counts are the imagery.

Census forms add the second idea: fields with a label and an answer. Reference pages read like a
form, with the field name (a flag or key) on the left and its answer on the right.

## Colour

Cool card-stock paper with ink, and a form-ink teal for the brand. The category colours are
semantic: each one means its category and nothing else, and nothing decorative uses them.

| Token | Light | Dark | Use |
|---|---|---|---|
| `--tc-paper` | `#F2F5F4` | `#0F1E1D` | page background |
| `--tc-surface` | `#FFFFFF` | `#162927` | code blocks, tables, the hero tally field |
| `--tc-ink` | `#17302E` | `#E3ECEA` | body text, headings |
| `--tc-ink-muted` | `#4A5F5C` | `#9FB3AF` | secondary text, captions |
| `--tc-rule` | `#C9D3D1` | `#2A3F3C` | hairlines, table borders (decorative only) |
| `--tc-control` | `#6B7C79` | `#7F9591` | input and button borders (3:1 floor) |
| `--tc-brand` | `#1D6B66` | `#5FC2B8` | links, active nav, focus ring, primary button |
| `--tc-managed` | `#1D6B66` | `#5FC2B8` | category: managed |
| `--tc-aws-managed` | `#5B6B82` | `#A3B3CC` | category: aws-managed |
| `--tc-other-iac` | `#6A4FA3` | `#B9A3E8` | category: other-iac |
| `--tc-derived` | `#5C6B68` | `#8FA09C` | category: derived |
| `--tc-unmanaged` | `#B43F2A` | `#F08A70` | category: unmanaged; the "writes state" warning |
| `--tc-undetermined` | `#8A5F00` | `#E2B44A` | category: undetermined |

Contrast measured on 2026-11-17 (WCAG 2.x formula). Every text-capable token is at least 4.5:1
on both `paper` and `surface` in its theme. The lowest light values: `aws-managed` 4.95 on paper,
`derived` 5.09, `undetermined` 5.15, `unmanaged` 5.19, `brand` 5.72. Dark values are all above
5.5. `rule` is decorative and is never the only edge of a control. The CI contrast check
([docs-site](docs-site.md#checks-shared)) keeps these numbers true.

Changes from the owner's preview: `undetermined` went from `#9A6B00` to `#8A5F00` to reach 4.5:1
as text, and `derived` was set at `#5C6B68` for the same reason.

`--vp-c-brand-1..3`, `--vp-c-bg`, `--vp-c-bg-soft`, `--vp-c-text-1..2` and `--vp-c-divider` map to
these tokens in `tokens.css`, so the default theme's nav, sidebar and search follow them.

## Category marks

Colour is never the only cue (WCAG 1.4.1). Each category has a colour and a shape, and a text
label always sits next to the mark.

| Category | Mark |
|---|---|
| managed | filled square |
| aws-managed | filled square with a lighter inner square |
| other-iac | square outline, 2 px |
| derived | small filled square, two thirds size, centred |
| unmanaged | filled square with a diagonal cut corner |
| undetermined | dashed square outline |

Marks are inline SVG at 1em with `aria-hidden="true"`; the label carries the meaning.

## Type

- Public Sans (variable, `@fontsource-variable/public-sans`, OFL): UI, headings and body. The
  US Web Design System made it for government forms, which fits the census idea, and its tabular
  figures line counts up in tables.
- IBM Plex Mono (`@fontsource/ibm-plex-mono`, OFL, weights 400 and 600): code, commands, flags and
  terminal transcripts only. Not for labels or counts.
- Counts use Public Sans with `font-variant-numeric: tabular-nums`.

Scale (rem, base 16 px, ratio 1.25): body 1 / line height 1.65, small 0.875, h4 1.125, h3 1.375,
h2 1.75, h1 2.25, home headline 3 on wide screens and 2.25 on phones. Weights: body 400, strong
600, headings 650, wordmark 750. Headings in sentence case. No all-caps labels and no tracked-out
eyebrows. Prose measure at most 72ch. Reference tables can be wider and scroll inside their own
container.

## Layout

The doc, reference and CLI pages keep VitePress's three columns (sidebar, content, outline).
Engineers know that wayfinding from other tool docs. The identity shows in colour, type, the
marks and the home page.

Home, wide screen. Left-aligned. The tally is the one bold element.

```
┌──────────────────────────────────────────────────────────────────────────┐
│ ▣ tfcensus     Guide  Reference  CLI   v0.4.0   GitHub        [search]   │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  Count what your Terraform     ┌ sample account, 2 regions ───────────┐  │
│  manages.                      │ managed       ■■■■■■■■■■■■■■■■  412   │  │
│                                │ aws-managed   ▣▣▣▣▣▣             158   │  │
│  tfcensus lists every unit     │ other-iac     □□                  61   │  │
│  and state in a Terragrunt     │ derived       ▪▪                  52   │  │
│  fleet and finds the AWS       │ unmanaged     ◩                   24   │  │
│  resources none of them        │ undetermined  ⬚                    9   │  │
│  manage. Read-only by default. │ one square = 25 resources             │  │
│                                └───────────────────────────────────────┘  │
│  $ mise use github:sassdavid/tfcensus      [copy]                        │
│  [Get started]  [Install]                                                │
│                                                                          │
│  What it answers                                                         │
│  Which resources does no unit manage?          tfcensus scan        Read │
│  Are the state buckets and lock tables safe?   tfcensus audit backend    │
│  What drifted, and in how many units?          tfcensus drift            │
│  Which unit manages this ARN?                  tfcensus whois            │
│  How do I move resources between units?        tfcensus move             │
│                                                                          │
│  footer: Apache-2.0, source, releases                                    │
└──────────────────────────────────────────────────────────────────────────┘
```

- The numbers in the wireframe are placeholders.
- The tally reads a committed fixture (`docs/.vitepress/theme/data/home-tally.json`, written by
  a generator from a scan golden), so the numbers are a real tfcensus output and not typed by
  hand. The caption says what one square stands for and that it is a sample.
- "What it answers" is a list of rows, question then command then link. It is not a card grid
  and has no numbers, because the rows are not a sequence.
- The getting-started page is a sequence (doctor, units, inventory, scan), so its steps are
  numbered.

Phone (about 400 px): the tally moves under the headline and install command, the rows stack
(question above command), and the nav collapses to VitePress's menu.

CLI reference page:

```
┌ sidebar ─────────┬ content ─────────────────────────────────┬ outline ───┐
│ CLI              │ # tfcensus audit backend                 │ Usage      │
│  units           │ Audit state buckets and lock tables.     │ Flags      │
│  inventory       │ ```sh                                    │ Formats    │
│  scan            │ tfcensus audit backend [flags]           │ Exit codes │
│  audit           │ ```                                      │ Examples   │
│   backend  ◂     │ Flags                                    │ Related    │
│  drift           │ ┌ flag ──────┬ type ─┬ default ┬ values … │            │
│  ...             │ │ --discover │ bool  │ false   │         │            │
│ Global flags     │ └────────────┴───────┴─────────┴──────── │            │
└──────────────────┴──────────────────────────────────────────┴────────────┘
```

Flag names in the first column use IBM Plex Mono at 0.875rem. Rows have a hairline rule and no
zebra striping.

## Components

| Component | Where | Notes |
|---|---|---|
| `HomeHero` | home | headline, lead, install command with copy button, two buttons, `CensusTally` |
| `CensusTally` | home, scan guide, scan concepts | props: rows from JSON, unit size, caption. Renders SVG rows with a text table for screen readers |
| `CategoryMark` | anywhere, `<CategoryMark kind="unmanaged" />` | mark plus label |
| `AnswerRows` | home | question, command, link |
| `Transcript` | guides | static terminal output included from goldens. No typing animation |
| `WritesState` | `move apply`, `move rollback`, `move mark-applied` pages and the move guide | a bordered note in `--tc-unmanaged`: "This command writes to S3 or DynamoDB." The generator adds it from `Annotations["docs.writes"]` |
| `SiteFooter` | `layout-bottom` | license, source, releases, sasskovacs.dev |

## Glyph and wordmark

- Glyph: a 3 by 3 tally, nine squares with 1 px gaps. Six managed, two aws-managed, one
  unmanaged in the bottom-right corner. It is drawn on a 16 px grid so the favicon stays sharp,
  with a monochrome variant for `mask-icon`.
- Wordmark: "tfcensus" in Public Sans 750, lowercase, set next to the glyph in the nav.
- Files: `docs/public/glyph.svg`, `favicon.svg`, `favicon-32.png`, `apple-touch-icon.png`,
  `site.webmanifest` with `theme_color` set to `--tc-brand` (light).

## Social cards

1200 by 630, generated at build time by `social.mjs` with resvg and the bundled Public Sans file
(its OFL text kept as `docs/.vitepress/fonts/PublicSans.LICENSE`). Paper background, glyph and
wordmark top left, the page title at 64 px weight 650 wrapping at two lines, the section name
under it in `ink-muted`, and a single tally row along the bottom edge in the category colours.
File names are content hashes.

## Code blocks

Shiki themes `github-light` and `github-dark`, with the block background set to
`--tc-surface`. The contrast check covers comment colour on that background (4.5:1). Copy button
on, line numbers off, and line highlighting for tutorial steps only.

## Motion

One moment: on the first home page load the tally fills row by row, about 600 ms in total.
`prefers-reduced-motion: reduce` shows it filled at once. Nothing else animates on its own. Hover
states change colour only.

## Accessibility

- WCAG 2.2 AA throughout. The focus ring is 2 px `--tc-brand` with a 2 px offset, and on brand
  fills it switches to `--tc-ink`.
- The tally has a visually hidden table with the same rows and counts.
- Tables scroll inside their own container and never widen the page.
- Every interactive target is at least 24 by 24 px (WCAG 2.5.8).
- The theme follows the system setting, and the VitePress switch overrides it.

## Review against generic defaults

Checked on 2026-11-17 against the defaults a generated docs site falls into.

- Card grid of features with icons: replaced by `AnswerRows`, because the home page answers
  questions, and a list of commands reads faster than cards.
- Big number with a small label and a gradient: the tally shows six numbers with their squares,
  and there are no gradients.
- Cream paper with a terracotta accent: replaced by cool card-stock and form-ink teal. The only
  warm colour is `unmanaged`, and it means unmanaged.
- Monospace for small data labels: counts use Public Sans tabular figures, and mono is kept for
  code.
- Inter, VitePress's default face: replaced by Public Sans, chosen for its forms heritage.
- Numbered markers: only on the getting-started steps, which are a sequence.

## Acceptance criteria

1. `tokens.css` defines every token above for both themes, and the contrast check passes.
2. The home page matches the wireframe at 1280 px and 400 px, with the tally read from the
   fixture.
3. Category marks render with their shapes and labels in the scan guide and the scan reference.
4. Glyph, favicons and the manifest are in `docs/public`, and the social cards follow the layout.
5. axe reports no violation, and the reduced-motion check shows the tally filled at once.
