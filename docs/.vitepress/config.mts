// Site configuration for https://tfcensus.sasskovacs.dev (docs/plan/specs/docs-site.md
// "Site configuration"). The sibling repo has the same shape with its own name.
import { readFileSync } from "node:fs";
import { defineConfig, type HeadConfig } from "vitepress";
import { sidebar } from "./sidebar.mts";
import { readTokens } from "./tokens.mjs";

const tool = "tfcensus";
const site = `https://${tool}.sasskovacs.dev`;
const repo = `https://github.com/sassdavid/${tool}`;
const description =
  "Count what your Terraform manages: units, states and the AWS resources no unit manages.";

const manifest = JSON.parse(
  readFileSync(new URL("../../.release-please-manifest.json", import.meta.url), "utf8"),
) as Record<string, string>;
const version = manifest["."];

// github-dark's comment colour is under 4.5:1 on the dark surface, so code comments take the
// ink-muted token of each theme (docs-design.md "Code blocks").
const tokens = readTokens(readFileSync(new URL("./theme/tokens.css", import.meta.url), "utf8"));
const commentColour = {
  "github-light": tokens.light["ink-muted"],
  "github-dark": tokens.dark["ink-muted"],
};

export default defineConfig({
  lang: "en",
  title: tool,
  description,
  cleanUrls: true,
  lastUpdated: true,
  sitemap: { hostname: site },
  srcExclude: [
    "plan/**",
    "engineering/**",
    "validation/**",
    "dogfood.md",
    "README.md",
    // M23 replaces the cobra pages with the nested reference and removes this line.
    "cli/**",
  ],
  // The IAM folder keeps README.md so GitHub shows it in the folder view.
  rewrites: { "iam/README.md": "iam/index.md" },
  head: [["link", { rel: "icon", type: "image/svg+xml", href: "/glyph.svg" }]],
  markdown: {
    theme: { light: "github-light", dark: "github-dark" },
    shikiSetup(highlighter) {
      for (const [name, colour] of Object.entries(commentColour)) {
        const theme = highlighter.getTheme(name);
        highlighter.loadThemeSync({
          ...theme,
          settings: theme.settings.map((s) => {
            const scopes = ([] as string[]).concat(s.scope ?? []);
            return scopes.some((scope) => scope.split(/[\s.,]+/).includes("comment"))
              ? { ...s, settings: { ...s.settings, foreground: colour } }
              : s;
          }),
        });
      }
    },
  },
  themeConfig: {
    logo: { light: "/glyph.svg", dark: "/glyph-dark.svg", alt: "" },
    siteTitle: tool,
    nav: [
      { text: "Guide", link: "/install", activeMatch: "^/(install|runbooks|ci)/?" },
      { text: "Reference", link: "/iam/", activeMatch: "^/iam/" },
      // M23 points this at /cli/ once the nested reference is published.
      { text: "CLI", link: `${repo}/tree/v${version}/docs/cli` },
      // M24 points this at /changelog.
      { text: `v${version}`, link: `${repo}/releases/tag/v${version}` },
    ],
    sidebar,
    socialLinks: [{ icon: "github", link: repo, ariaLabel: `${tool} on GitHub` }],
    search: { provider: "local" },
    editLink: {
      pattern: `${repo}/edit/main/docs/:path`,
      text: "Edit this page on GitHub",
    },
    outline: { level: [2, 3] },
  },
  transformPageData(pageData) {
    if (!pageData.frontmatter.description) {
      throw new Error(
        `${pageData.relativePath}: frontmatter needs a description (search and llms.txt use it)`,
      );
    }
  },
  transformHead({ pageData, title }) {
    const path = pageData.relativePath.replace(/(^|\/)index\.md$/, "$1").replace(/\.md$/, "");
    const url = `${site}/${path}`;
    const text = (pageData.frontmatter.description as string | undefined) ?? description;
    const head: HeadConfig[] = [
      ["link", { rel: "canonical", href: url }],
      ["meta", { property: "og:type", content: "website" }],
      ["meta", { property: "og:site_name", content: tool }],
      ["meta", { property: "og:title", content: title }],
      ["meta", { property: "og:description", content: text }],
      ["meta", { property: "og:url", content: url }],
      ["meta", { name: "twitter:card", content: "summary" }],
      ["meta", { name: "twitter:title", content: title }],
      ["meta", { name: "twitter:description", content: text }],
    ];
    return head;
  },
});
