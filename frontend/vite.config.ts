import { writeFileSync } from "node:fs";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

// packageOf 由 Rollup 的 module id 取出其所屬 npm 套件的名稱與目錄, 不屬於
// node_modules 者回傳 null. 巢狀的 node_modules 以最內層為準.
function packageOf(id: string): { name: string; dir: string } | null {
  const path = id.replace(/^\0/, "").replace(/\?.*$/, "");
  const marker = "/node_modules/";
  const at = path.lastIndexOf(marker);
  if (at < 0) {
    return null;
  }
  const rest = path.slice(at + marker.length).split("/");
  const name = rest[0].startsWith("@") ? `${rest[0]}/${rest[1]}` : rest[0];
  return { name, dir: path.slice(0, at + marker.length) + name };
}

// bundledPackages 於環境變數 GPUMON_NPM_PACKAGES 指定路徑時, 將實際打包進產出
// 的 npm 套件寫入該檔案, 每行為 "<名稱>\t<目錄>", 依名稱排序. 此檔案為
// build.sh 產生第三方授權清單的輸入.
function bundledPackages(): Plugin {
  const out = process.env.GPUMON_NPM_PACKAGES;
  return {
    name: "gpumon-bundled-packages",
    apply: "build",
    generateBundle(_options, bundle) {
      if (!out) {
        return;
      }
      const found = new Map<string, string>();
      for (const item of Object.values(bundle)) {
        if (item.type !== "chunk") {
          continue;
        }
        for (const [id, info] of Object.entries(item.modules)) {
          const pkg = packageOf(id);
          if (pkg && info.renderedLength > 0) {
            found.set(pkg.name, pkg.dir);
          }
        }
      }
      const lines = [...found.entries()]
        .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
        .map(([name, dir]) => `${name}\t${dir}\n`);
      writeFileSync(out, lines.join(""));
    },
  };
}

export default defineConfig({
  plugins: [react(), bundledPackages()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
