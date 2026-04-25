<script setup>
// Placeholder App.vue — the real shell (Titlebar / Toolbar / LeftRail /
// CanvasHead / LegendBar / Shell) lands in the frontend-foundations
// mission. This stub exists so `wails dev` boots a window during local
// iteration on the Go backend.
</script>

<template>
  <main class="placeholder">
    <h1>kaneaz-harness</h1>
    <p>Frontend foundations not yet implemented. <code>wails dev</code> hot reload is wired up.</p>
  </main>
</template>

<style>
:root {
  color-scheme: dark;
  font-family: ui-sans-serif, -apple-system, BlinkMacSystemFont, sans-serif;
}

body {
  margin: 0;
  background: #0A0A0B;
  color: #F4F1EA;
}

.placeholder {
  min-height: 100vh;
  display: grid;
  place-items: center;
  text-align: center;
  padding: 2rem;
}

.placeholder h1 {
  font-size: 2rem;
  font-weight: 500;
  margin: 0 0 0.5rem;
  letter-spacing: -0.01em;
}

.placeholder p {
  color: #A0A0A8;
  margin: 0;
}

.placeholder code {
  font-family: ui-monospace, "SF Mono", Menlo, monospace;
  background: #17171A;
  padding: 0.125rem 0.375rem;
  border-radius: 4px;
}
</style>
