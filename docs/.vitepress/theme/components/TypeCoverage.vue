<script setup lang="ts">
// The type coverage table: one row per Terraform resource type, from data/type-coverage.json,
// which scripts/gensite writes from the typemap. Filters run in the browser; the first render
// shows the mapped types, so the built page stays small.
import { computed, ref } from "vue";
import data from "../data/type-coverage.json";

interface Row {
  tf: string;
  svc: string;
  mapped: boolean;
  cfn?: string[];
  config?: string;
  re?: string[];
  layers?: string[];
  keys?: string[];
  note?: string;
}

const rows = data.rows as Row[];
const query = ref("");
const status = ref<"mapped" | "undetermined" | "all">("mapped");
const layer = ref("");

const shown = computed(() => {
  const q = query.value.trim().toLowerCase();
  return rows.filter((r) => {
    if (status.value === "mapped" && !r.mapped) return false;
    if (status.value === "undetermined" && r.mapped) return false;
    if (layer.value && !(r.layers ?? []).includes(layer.value)) return false;
    if (!q) return true;
    return [r.tf, r.svc, ...(r.cfn ?? []), r.config ?? "", ...(r.re ?? [])].some((v) =>
      v.toLowerCase().includes(q),
    );
  });
});
</script>

<template>
  <div class="tc-coverage">
    <form class="tc-coverage__filters" @submit.prevent>
      <label>
        <span>Search</span>
        <input v-model="query" type="search" placeholder="aws_s3_bucket, AWS::IAM::Role, ec2" />
      </label>
      <label>
        <span>Status</span>
        <select v-model="status">
          <option value="mapped">mapped ({{ data.mapped }})</option>
          <option value="undetermined">undetermined ({{ data.types - data.mapped }})</option>
          <option value="all">all ({{ data.types }})</option>
        </select>
      </label>
      <label>
        <span>Listed by</span>
        <select v-model="layer">
          <option value="">any layer</option>
          <option v-for="l in data.layers" :key="l" :value="l">{{ l }}</option>
        </select>
      </label>
    </form>
    <p class="tc-coverage__count" aria-live="polite">{{ shown.length }} types</p>
    <div class="tc-coverage__scroll">
      <table>
        <thead>
          <tr>
            <th scope="col">Terraform type</th>
            <th scope="col">CloudFormation type</th>
            <th scope="col">Listed by</th>
            <th scope="col">Match keys</th>
            <th scope="col">Notes</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in shown" :key="r.tf">
            <td>
              <code>{{ r.tf }}</code>
            </td>
            <td>
              <template v-if="r.mapped">
                <code v-for="c in r.cfn" :key="c" class="tc-coverage__item">{{ c }}</code>
              </template>
              <span v-else class="tc-coverage__muted">undetermined</span>
            </td>
            <td>{{ (r.layers ?? []).join(", ") }}</td>
            <td>{{ (r.keys ?? []).join(", ") }}</td>
            <td>{{ r.note ?? "" }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.tc-coverage__filters {
  display: flex;
  flex-wrap: wrap;
  gap: 12px 24px;
  margin: 16px 0 8px;
}

.tc-coverage__filters label {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 0.875rem;
}

.tc-coverage__filters input,
.tc-coverage__filters select {
  min-width: 12rem;
  max-width: 100%;
  padding: 4px 8px;
  border: 1px solid var(--tc-control);
  border-radius: 4px;
  background: var(--tc-surface);
  color: var(--tc-ink);
}

.tc-coverage__count {
  margin: 8px 0;
  color: var(--tc-ink-muted);
  font-size: 0.875rem;
}

.tc-coverage__scroll {
  overflow-x: auto;
}

.tc-coverage__scroll table {
  display: table;
  width: 100%;
  margin: 0;
}

.tc-coverage__item {
  display: block;
  margin: 2px 0;
  width: fit-content;
}

.tc-coverage__muted {
  color: var(--tc-ink-muted);
}
</style>
