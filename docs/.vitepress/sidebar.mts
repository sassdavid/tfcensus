// The sidebar: hand-written sections. The CLI tree from cli/commands.json joins it once
// scripts/gendocs writes the nested reference (docs-site.md "CLI reference").
import type { DefaultTheme } from "vitepress";

export const sidebar: DefaultTheme.Sidebar = [
  {
    text: "Start",
    items: [{ text: "Install", link: "/install" }],
  },
  {
    text: "Guides",
    items: [
      { text: "Move resources between units", link: "/runbooks/move" },
      { text: "Publish reports with notify", link: "/runbooks/notify" },
      { text: "AI summary of drift in CI", link: "/ci/ai-summary" },
    ],
  },
  {
    text: "Reference",
    items: [{ text: "Read-only IAM policy", link: "/iam/" }],
  },
];
