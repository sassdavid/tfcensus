<script setup lang="ts">
// The note on the pages of commands that write: scripts/gendocs adds it from the command's
// docs.writes annotation, which lists the stores it writes to.
import { computed } from "vue";

const props = defineProps<{ stores: string }>();
const text = computed(() => {
  const list = props.stores.split(",").filter(Boolean);
  const last = list.pop();
  return list.length ? `${list.join(", ")} or ${last}` : (last ?? "");
});
</script>

<template>
  <div class="tc-writes" role="note">
    <p>
      <strong>This command writes to {{ text }}.</strong> Every other command is read-only: see
      <a href="/concepts/safety">the safety model</a>.
    </p>
  </div>
</template>

<style scoped>
.tc-writes {
  margin: 16px 0;
  padding: 8px 16px;
  border: 1px solid var(--tc-unmanaged);
  border-left-width: 4px;
  border-radius: 4px;
}

.tc-writes p {
  margin: 8px 0;
}
</style>
