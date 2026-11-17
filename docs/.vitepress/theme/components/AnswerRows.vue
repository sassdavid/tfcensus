<script setup lang="ts">
// "What it answers": one row per question with the command that answers it and a link. The rows
// are not a sequence, so they carry no numbers.
import { useData, withBase } from "vitepress";

interface Answer {
  question: string;
  command: string;
  link: string;
}

const { frontmatter } = useData();
const answers = (frontmatter.value.answers ?? []) as Answer[];

function href(link: string): string {
  return /^https?:/.test(link) ? link : withBase(link);
}
</script>

<template>
  <section class="tc-answers" aria-labelledby="tc-answers-title">
    <h2 id="tc-answers-title" class="tc-answers__title">What it answers</h2>
    <ul class="tc-answers__list">
      <li v-for="answer in answers" :key="answer.command" class="tc-answers__row">
        <span class="tc-answers__question">{{ answer.question }}</span>
        <code class="tc-answers__command">{{ answer.command }}</code>
        <a class="tc-answers__link" :href="href(answer.link)"
          >Read<span class="tc-sr-only"> about {{ answer.command }}</span></a
        >
      </li>
    </ul>
  </section>
</template>

<style scoped>
.tc-answers {
  padding: 1rem 0 3rem;
}

.tc-answers__title {
  margin: 0 0 0.75rem;
  color: var(--tc-ink);
  font-size: 1.75rem;
  font-weight: 650;
  letter-spacing: 0;
}

.tc-answers__list {
  margin: 0;
  padding: 0;
  list-style: none;
  border-top: 1px solid var(--tc-rule);
}

.tc-answers .tc-answers__row {
  max-width: none;
  margin: 0;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 17rem 4rem;
  column-gap: 2rem;
  gap: 1rem;
  align-items: baseline;
  padding: 0.85rem 0;
  border-bottom: 1px solid var(--tc-rule);
}

.tc-answers__question {
  color: var(--tc-ink);
}

.tc-answers__command {
  padding: 0;
  background: none;
  color: var(--tc-ink);
  font-family: var(--vp-font-family-mono);
  font-size: 0.9375rem;
  white-space: nowrap;
}

.tc-answers__link {
  display: inline-flex;
  align-items: center;
  min-height: 1.5rem;
  color: var(--tc-brand);
  font-weight: 600;
  text-decoration: underline;
  text-underline-offset: 2px;
}

.tc-answers__link:hover {
  color: var(--tc-ink);
}

@media (max-width: 639px) {
  .tc-answers .tc-answers__row {
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 0.25rem 1rem;
  }

  .tc-answers__question {
    grid-column: 1 / -1;
  }
}
</style>
