<script setup lang="ts">
// The footer in the layout-bottom slot: license, source and releases. On pages with a sidebar it
// starts where the content column starts.
import { useSidebar } from "vitepress/theme-without-fonts";

const { hasSidebar } = useSidebar();
const repo = "https://github.com/sassdavid/tfcensus";
</script>

<template>
  <footer class="tc-footer" :class="{ 'has-sidebar': hasSidebar }">
    <ul class="tc-footer__links">
      <li><a :href="`${repo}/blob/main/LICENSE`">Apache-2.0</a></li>
      <li><a :href="repo">Source</a></li>
      <li><a :href="`${repo}/releases`">Releases</a></li>
      <li><a href="https://sasskovacs.dev">sasskovacs.dev</a></li>
    </ul>
  </footer>
</template>

<style scoped>
.tc-footer {
  padding: 1.5rem 1.5rem 2rem;
  border-top: 1px solid var(--tc-rule);
  color: var(--tc-ink-muted);
  font-size: 0.875rem;
}

.tc-footer__links {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem 1.5rem;
  max-width: 1152px;
  margin: 0 auto;
  padding: 0;
  list-style: none;
}

.tc-footer a {
  display: inline-flex;
  align-items: center;
  min-height: 1.5rem;
  color: var(--tc-ink-muted);
  text-decoration: underline;
  text-underline-offset: 2px;
}

.tc-footer a:hover {
  color: var(--tc-brand);
}

@media (min-width: 960px) {
  .tc-footer.has-sidebar {
    padding-left: calc(var(--vp-sidebar-width) + 2rem);
  }
}

@media (min-width: 1440px) {
  .tc-footer.has-sidebar {
    padding-left: calc(
      (100% - (var(--vp-layout-max-width) - 64px)) / 2 + var(--vp-sidebar-width) - 32px + 2rem
    );
  }
}
</style>
