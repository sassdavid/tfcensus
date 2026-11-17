// Writes public/llms.txt in the llmstxt.org format from the sidebar and each page's frontmatter
// description, and exits 1 when the committed file was stale (docs-site.md "llms.txt"). Run it
// with node, which strips the types. Shared with the sibling repo.
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import config from "./config.mts";

// SidebarItem is the part of a VitePress sidebar item that llms.txt reads.
export type SidebarItem = { text?: string; link?: string; items?: SidebarItem[] };

// Page is what llms.txt shows for one page.
export type Page = { title?: string; description?: string };

// frontmatter reads the flat "key: value" lines of a Markdown file's frontmatter.
export function frontmatter(markdown: string): Record<string, string> {
  const match = /^---\r?\n([\s\S]*?)\r?\n---/.exec(markdown);
  const out: Record<string, string> = {};
  for (const line of match ? match[1].split(/\r?\n/) : []) {
    const kv = /^([A-Za-z][\w-]*):\s*(.*)$/.exec(line);
    if (kv && kv[2] !== "") {
      out[kv[1]] = kv[2]
        .replace(/^"(.*)"$/, "$1")
        .replace(/\\"/g, '"')
        .replace(/^'(.*)'$/, "$1");
    }
  }
  return out;
}

// pageFiles returns the Markdown files that can hold the page at link, relative to docs/.
export function pageFiles(link: string): string[] {
  const path = link.replace(/#.*$/, "").replace(/^\//, "");
  if (path === "" || path.endsWith("/")) {
    return [`${path}index.md`, `${path}README.md`];
  }
  return [`${path}.md`, `${path}/index.md`];
}

function flatten(items: SidebarItem[]): SidebarItem[] {
  return items.flatMap((i) => [...(i.link ? [i] : []), ...flatten(i.items ?? [])]);
}

// buildLlms returns llms.txt: the title, the summary, then one section per sidebar group with a
// link and description per page. A group with a link of its own lists that page first.
export function buildLlms(opts: {
  site: string;
  title: string;
  summary: string;
  groups: SidebarItem[];
  page: (link: string) => Page | undefined;
}): string {
  const lines = [`# ${opts.title}`, "", `> ${opts.summary}`];
  for (const group of opts.groups) {
    lines.push("", `## ${group.text ?? ""}`, "");
    for (const item of flatten(group.link ? [group] : (group.items ?? []))) {
      const link = item.link ?? "";
      const page = opts.page(link);
      const url = /^https?:/.test(link) ? link : opts.site + link;
      const title = page?.title ?? item.text ?? link;
      lines.push(`- [${title}](${url})` + (page?.description ? `: ${page.description}` : ""));
    }
  }
  return lines.join("\n") + "\n";
}

// readPage reads the frontmatter of the page at link from the docs directory.
export function readPage(link: string): Page | undefined {
  for (const file of pageFiles(link)) {
    const url = new URL(`../${file}`, import.meta.url);
    if (existsSync(url)) {
      const fm = frontmatter(readFileSync(url, "utf8"));
      return { title: fm.title, description: fm.description };
    }
  }
  return undefined;
}

// siteLlms returns llms.txt for this site.
export function siteLlms(): string {
  const sidebar = config.themeConfig?.sidebar;
  const groups = (
    Array.isArray(sidebar) ? sidebar : Object.values(sidebar ?? {}).flat()
  ) as SidebarItem[];
  return buildLlms({
    site: config.sitemap?.hostname ?? "",
    title: config.title ?? "",
    summary: config.description ?? "",
    groups,
    page: readPage,
  });
}

if (import.meta.main) {
  const out = new URL("../public/llms.txt", import.meta.url);
  const want = siteLlms();
  const have = existsSync(out) ? readFileSync(out, "utf8") : "";
  if (have !== want) {
    writeFileSync(out, want);
    console.error("docs/public/llms.txt was stale and has been regenerated; review and commit it");
    process.exit(1);
  }
}
