// The site theme: VitePress's default theme without its Inter font, the tool's tokens and every
// component in ./components registered under its file name. Shared with the sibling repo.
import type { Theme } from "vitepress";
import DefaultTheme from "vitepress/theme-without-fonts";
import { h, type Component } from "vue";
import "./tokens.css";

const components = import.meta.glob<{ default: Component }>("./components/*.vue", {
  eager: true,
});

function component(name: string): Component {
  const mod = components[`./components/${name}.vue`];
  if (!mod) {
    throw new Error(`theme: components/${name}.vue is missing`);
  }
  return mod.default;
}

export default {
  extends: DefaultTheme,
  Layout() {
    return h(DefaultTheme.Layout, null, {
      "layout-bottom": () => h(component("SiteFooter")),
    });
  },
  enhanceApp({ app }) {
    for (const [path, mod] of Object.entries(components)) {
      app.component(path.slice("./components/".length, -".vue".length), mod.default);
    }
  },
} satisfies Theme;
