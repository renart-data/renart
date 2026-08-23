import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { defineConfig, type Plugin } from "vite";
import { TanStackRouterVite } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import Icons from "unplugin-icons/vite";

const require = createRequire(import.meta.url);
const PROXY_TARGET = process.env.PROXY_TARGET ?? "http://127.0.0.1:3000";

// The backend's SameOriginGuard rejects state-changing requests whose Origin
// doesn't match its Host. `changeOrigin` already rewrites the forwarded Host to
// the proxy target, but leaves the browser's Origin intact — so when the app is
// reached from another machine (HOST=0.0.0.0), Origin (dev-machine IP) and Host
// (proxy target) disagree and POSTs are rejected. The dev proxy is effectively
// same-origin to the backend, so align the forwarded Origin with the target.
const alignProxyOrigin: import("vite").ProxyOptions["configure"] = (proxy) => {
  proxy.on("proxyReq", (proxyReq) => {
    if (proxyReq.getHeader("origin")) {
      proxyReq.setHeader("origin", PROXY_TARGET);
    }
  });
};

function prepareMonacoAssetsPlugin(): Plugin {
  let hasPreparedAssets = false;

  const prepareAssets = () => {
    if (hasPreparedAssets) {
      return;
    }

    let packageRoot = path.dirname(require.resolve("monaco-editor"));
    while (
      packageRoot !== path.dirname(packageRoot) &&
      !fs.existsSync(path.join(packageRoot, "package.json"))
    ) {
      packageRoot = path.dirname(packageRoot);
    }
    const sourceDir = path.join(packageRoot, "min", "vs");
    if (!fs.existsSync(path.join(sourceDir, "loader.js"))) {
      throw new Error(`Monaco AMD assets were not found under ${sourceDir}`);
    }
    const targetDir = path.resolve(__dirname, "public/monaco/vs");

    fs.rmSync(targetDir, { recursive: true, force: true });
    fs.mkdirSync(path.dirname(targetDir), { recursive: true });
    fs.cpSync(sourceDir, targetDir, { recursive: true, force: true });

    hasPreparedAssets = true;
  };

  return {
    name: "prepare-monaco-assets",
    configResolved() {
      prepareAssets();
    },
  };
}

export default defineConfig({
  plugins: [
    TanStackRouterVite({ target: "react", autoCodeSplitting: true }),
    react(),
    Icons({ compiler: "jsx", jsx: "react" }),
    prepareMonacoAssetsPlugin(),
  ],
  build: {
    manifest: true,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes("node_modules")) {
            return undefined;
          }

          if (id.includes("reactflow")) {
            return "graph-vendor";
          }

          if (id.includes("@tanstack/react-router") || id.includes("@tanstack/router-core")) {
            return "router-vendor";
          }

          if (id.includes("jotai") || id.includes("@base-ui/react") || id.includes("radix-ui")) {
            return "ui-vendor";
          }

          if (id.includes("recharts") || id.includes("victory-vendor") || id.includes("d3-")) {
            return "chart-vendor";
          }

          if (id.includes("@monaco-editor") || id.includes("monaco-editor")) {
            return "monaco-vendor";
          }

          if (
            id.includes("react-markdown") ||
            id.includes("remark-") ||
            id.includes("mdast-util-") ||
            id.includes("micromark") ||
            id.includes("unified")
          ) {
            return "markdown-vendor";
          }

          return undefined;
        },
      },
    },
  },
  resolve: {
    alias: {
      "@": path.resolve(__dirname),
    },
  },
  server: {
    port: 5173,
    proxy: {
      "^/api/pipelines/.*/materialize/stream$": {
        target: PROXY_TARGET,
        changeOrigin: true,
        configure: alignProxyOrigin,
        timeout: 0,
        proxyTimeout: 0,
      },
      "^/api/assets/.*/materialize/stream$": {
        target: PROXY_TARGET,
        changeOrigin: true,
        configure: alignProxyOrigin,
        timeout: 0,
        proxyTimeout: 0,
      },
      "/api": {
        target: PROXY_TARGET,
        changeOrigin: true,
        configure: alignProxyOrigin,
        ws: true,
      },
    },
  },
  preview: {
    port: 5173,
  },
});
