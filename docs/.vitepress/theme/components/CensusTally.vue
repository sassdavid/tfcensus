<script lang="ts">
// The fill plays once per visit, not on every return to the home page.
let played = false;
</script>

<script setup lang="ts">
// Isotype rows: one mark per unit of resources, one row per scan category, with its count.
// The marks are hidden from screen readers, which read the table instead.
import { computed, onMounted } from "vue";
import CategoryMark, { type Category } from "./CategoryMark.vue";

export interface TallyRow {
  category: Category;
  count: number;
}

const props = defineProps<{ rows: TallyRow[]; unit: number; caption: string }>();

const animate = !played;
onMounted(() => {
  played = true;
});

const marks = computed(() =>
  props.rows.map((row) => ({ ...row, marks: Math.ceil(row.count / props.unit) })),
);
const unitText = computed(() =>
  props.unit === 1 ? "one square = 1 resource" : `one square = ${props.unit} resources`,
);
</script>

<template>
  <figure class="tc-tally" :class="{ 'tc-tally--animate': animate }">
    <figcaption class="tc-tally__caption">{{ caption }}</figcaption>
    <div class="tc-tally__rows" aria-hidden="true">
      <div
        v-for="(row, i) in marks"
        :key="row.category"
        class="tc-tally__row"
        :style="{ '--row': i }"
      >
        <span class="tc-tally__label">{{ row.category }}</span>
        <span class="tc-tally__marks">
          <CategoryMark v-for="n in row.marks" :key="n" :kind="row.category" bare />
        </span>
        <span class="tc-tally__count">{{ row.count }}</span>
      </div>
    </div>
    <table class="tc-sr-only">
      <caption>
        {{ caption
        }},
        {{ unitText }}
      </caption>
      <thead>
        <tr>
          <th scope="col">Category</th>
          <th scope="col">Resources</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in rows" :key="row.category">
          <th scope="row">{{ row.category }}</th>
          <td>{{ row.count }}</td>
        </tr>
      </tbody>
    </table>
    <p class="tc-tally__unit" aria-hidden="true">{{ unitText }}</p>
  </figure>
</template>

<style scoped>
.tc-tally {
  position: relative;
  margin: 0;
  padding: 1.75rem 1.25rem 1rem;
  border: 1px solid var(--tc-rule);
  border-radius: 2px;
  background: var(--tc-surface);
}

.tc-tally__caption {
  position: absolute;
  top: -0.7em;
  left: 0.9rem;
  padding: 0 0.4rem;
  background: var(--tc-surface);
  color: var(--tc-ink-muted);
  font-size: 0.875rem;
  line-height: 1.4;
}

.tc-tally__rows {
  display: grid;
  gap: 0.5rem;
}

.tc-tally__row {
  display: grid;
  grid-template-columns: 7.5rem 1fr 2.5rem;
  align-items: center;
  gap: 0.75rem;
}

.tc-tally__label {
  color: var(--tc-ink);
  font-size: 0.9375rem;
}

.tc-tally__marks {
  display: flex;
  flex-wrap: wrap;
  gap: 3px;
  font-size: 1.125rem;
  line-height: 1;
}

.tc-tally__count {
  color: var(--tc-ink);
  font-weight: 600;
  text-align: right;
}

.tc-tally__unit {
  margin: 0.9rem 0 0;
  color: var(--tc-ink-muted);
  font-size: 0.875rem;
}

.tc-tally--animate .tc-tally__marks {
  animation: tc-tally-fill 240ms ease-out both;
  animation-delay: calc(var(--row) * 70ms);
  transform-origin: left center;
}

@keyframes tc-tally-fill {
  from {
    clip-path: inset(0 100% 0 0);
  }
  to {
    clip-path: inset(0 0 0 0);
  }
}

@media (prefers-reduced-motion: reduce) {
  .tc-tally--animate .tc-tally__marks {
    animation: none;
  }
}

@media (max-width: 420px) {
  .tc-tally {
    padding-inline: 0.9rem;
  }

  .tc-tally__row {
    grid-template-columns: 6.5rem 1fr 2rem;
    gap: 0.5rem;
  }
}
</style>
