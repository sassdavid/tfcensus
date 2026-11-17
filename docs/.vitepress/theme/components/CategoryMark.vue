<script setup lang="ts">
// A scan category's mark: a colour and a shape, so colour is never the only cue (WCAG 1.4.1).
// The label carries the meaning; with bare the caller prints the label itself.
import { computed } from "vue";

export type Category =
  | "managed"
  | "aws-managed"
  | "other-iac"
  | "derived"
  | "unmanaged"
  | "undetermined";

const props = defineProps<{ kind: Category; label?: string; bare?: boolean }>();
const text = computed(() => props.label ?? props.kind);
</script>

<template>
  <span class="tc-mark" :class="`tc-mark--${kind}`">
    <svg class="tc-mark__svg" viewBox="0 0 16 16" width="1em" height="1em" aria-hidden="true">
      <template v-if="kind === 'managed'">
        <rect x="1" y="1" width="14" height="14" fill="currentColor" />
      </template>
      <template v-else-if="kind === 'aws-managed'">
        <rect x="1" y="1" width="14" height="14" fill="currentColor" />
        <rect x="5" y="5" width="6" height="6" class="tc-mark__inner" />
      </template>
      <template v-else-if="kind === 'other-iac'">
        <rect
          x="2"
          y="2"
          width="12"
          height="12"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
        />
      </template>
      <template v-else-if="kind === 'derived'">
        <rect x="3.5" y="3.5" width="9" height="9" fill="currentColor" />
      </template>
      <template v-else-if="kind === 'unmanaged'">
        <polygon points="1,1 15,1 15,9 9,15 1,15" fill="currentColor" />
      </template>
      <template v-else>
        <rect
          x="2"
          y="2"
          width="12"
          height="12"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-dasharray="3 2"
        />
      </template>
    </svg>
    <span v-if="!bare" class="tc-mark__label">{{ text }}</span>
  </span>
</template>

<style scoped>
.tc-mark {
  display: inline-flex;
  align-items: center;
  gap: 0.35em;
  white-space: nowrap;
}

.tc-mark__svg {
  flex: none;
  vertical-align: -0.125em;
}

.tc-mark--managed .tc-mark__svg {
  color: var(--tc-managed);
}

.tc-mark--aws-managed .tc-mark__svg {
  color: var(--tc-aws-managed);
}

.tc-mark--other-iac .tc-mark__svg {
  color: var(--tc-other-iac);
}

.tc-mark--derived .tc-mark__svg {
  color: var(--tc-derived);
}

.tc-mark--unmanaged .tc-mark__svg {
  color: var(--tc-unmanaged);
}

.tc-mark--undetermined .tc-mark__svg {
  color: var(--tc-undetermined);
}

.tc-mark__inner {
  fill: var(--tc-surface);
  opacity: 0.6;
}
</style>
