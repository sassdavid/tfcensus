<script setup lang="ts">
// The home page: headline, lead, install command, actions and the census tally, read from the
// page's frontmatter and the tally fixture written by scripts/gensite.
import { useData, withBase } from "vitepress";
import { ref } from "vue";
import tally from "../data/home-tally.json";
import CensusTally, { type TallyRow } from "./CensusTally.vue";

interface Action {
  text: string;
  link: string;
  theme?: "brand" | "alt";
}

const { frontmatter } = useData();
const copied = ref(false);

async function copy(text: string) {
  await navigator.clipboard.writeText(text);
  copied.value = true;
  setTimeout(() => (copied.value = false), 1500);
}

function href(link: string): string {
  return /^https?:/.test(link) ? link : withBase(link);
}

const actions = (frontmatter.value.actions ?? []) as Action[];
const rows = tally.rows as TallyRow[];
</script>

<template>
  <section class="tc-hero">
    <div class="tc-hero__text">
      <h1 class="tc-hero__headline">{{ frontmatter.headline }}</h1>
      <p class="tc-hero__lead">{{ frontmatter.lead }}</p>
      <div class="tc-hero__install">
        <code
          ><span class="tc-hero__prompt" aria-hidden="true">$ </span>{{ frontmatter.install }}</code
        >
        <button
          type="button"
          class="tc-hero__copy"
          :aria-label="`Copy the install command: ${frontmatter.install}`"
          @click="copy(frontmatter.install)"
        >
          {{ copied ? "Copied" : "Copy" }}
        </button>
        <span class="tc-sr-only" aria-live="polite">{{ copied ? "Copied" : "" }}</span>
      </div>
      <div class="tc-hero__actions">
        <a
          v-for="action in actions"
          :key="action.link"
          class="tc-button"
          :class="action.theme ?? 'brand'"
          :href="href(action.link)"
          >{{ action.text }}</a
        >
      </div>
    </div>
    <CensusTally class="tc-hero__tally" :rows="rows" :unit="tally.unit" :caption="tally.caption" />
  </section>
</template>

<style scoped>
.tc-hero {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 27rem);
  gap: 3rem;
  align-items: center;
  padding: 3.5rem 0 3rem;
}

.tc-hero__headline {
  margin: 0;
  color: var(--tc-ink);
  font-size: 3rem;
  font-weight: 650;
  line-height: 1.1;
  letter-spacing: 0;
}

.tc-hero__lead {
  max-width: 34rem;
  margin: 1.25rem 0 0;
  color: var(--tc-ink-muted);
  font-size: 1.125rem;
  line-height: 1.6;
}

.tc-hero__install {
  display: inline-flex;
  align-items: center;
  gap: 0.75rem;
  max-width: 100%;
  margin-top: 1.75rem;
  padding: 0.5rem 0.5rem 0.5rem 0.9rem;
  border: 1px solid var(--tc-rule);
  border-radius: 2px;
  background: var(--tc-surface);
}

.tc-hero__install code {
  overflow-wrap: anywhere;
  color: var(--tc-ink);
  font-family: var(--vp-font-family-mono);
  font-size: 0.9375rem;
}

.tc-hero__prompt {
  color: var(--tc-ink-muted);
}

.tc-hero__copy {
  flex: none;
  min-width: 4rem;
  min-height: 2rem;
  padding: 0 0.75rem;
  border: 1px solid var(--tc-control);
  border-radius: 2px;
  background: var(--tc-surface);
  color: var(--tc-ink);
  font-size: 0.875rem;
}

.tc-hero__copy:hover {
  border-color: var(--tc-brand);
  color: var(--tc-brand);
}

.tc-hero__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  margin-top: 1.5rem;
}

.tc-button {
  display: inline-flex;
  align-items: center;
  min-height: 2.75rem;
  padding: 0 1.25rem;
  border: 1px solid var(--tc-brand);
  border-radius: 2px;
  font-weight: 600;
  text-decoration: none;
  transition:
    background-color 0.2s,
    color 0.2s,
    border-color 0.2s;
}

.tc-button.brand {
  background: var(--tc-brand);
  color: var(--tc-paper);
}

.tc-button.brand:hover {
  border-color: var(--tc-ink);
  background: var(--tc-ink);
}

.tc-button.alt {
  border-color: var(--tc-control);
  background: var(--tc-surface);
  color: var(--tc-ink);
}

.tc-button.alt:hover {
  border-color: var(--tc-brand);
  color: var(--tc-brand);
}

@media (max-width: 959px) {
  .tc-hero {
    grid-template-columns: minmax(0, 1fr);
    gap: 2.5rem;
    padding-top: 2rem;
  }

  .tc-hero__headline {
    font-size: 2.25rem;
  }
}
</style>
