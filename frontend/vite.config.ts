import path from "path";
import { defineConfig, mergeConfig, searchForWorkspaceRoot } from "vite";
import wails from "@wailsio/runtime/plugins/vite";
import { appBuildConfig } from "./vite.base.ts";

export default defineConfig(mergeConfig(appBuildConfig("node_modules"), {
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
    fs: {
      allow: [
        searchForWorkspaceRoot(process.cwd()),
        path.resolve(import.meta.dirname, "../internal/publicweb/composer-emojis.json"),
      ],
    },
  },
  plugins: [wails("./bindings")],
}));
