import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Relative base: the shell serves the build from an embedded FS, not from a
// web root, so absolute asset paths would not resolve.
export default defineConfig({
  base: "./",
  plugins: [react()],
  build: {
    outDir: "dist",
    // Clear stale hashed bundles on every build. This also removes the
    // committed dist/.gitkeep that keeps `go:embed all:frontend/dist` compiling
    // on a fresh checkout, so the build script writes it back afterwards.
    emptyOutDir: true,
  },
});
