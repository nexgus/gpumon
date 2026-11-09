// 預覽頁的 Vite 設定: 把 src/api/backend.ts 換成 src/preview/mockBackend.ts, 以假資料
// 驅動前端供視覺核對. 只供開發伺服器使用, 正式建置 (vite.config.ts) 不含預覽頁.
import { fileURLToPath } from "node:url";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

const real = fileURLToPath(new URL("./src/api/backend.ts", import.meta.url));
const fake = fileURLToPath(new URL("./src/preview/mockBackend.ts", import.meta.url));

function mockBackend(): Plugin {
  return {
    name: "gpumon-mock-backend",
    enforce: "pre",
    async resolveId(source, importer, options) {
      const r = await this.resolve(source, importer, { ...options, skipSelf: true });
      if (r && r.id === real) {
        return fake;
      }
      return null;
    },
  };
}

export default defineConfig({
  plugins: [mockBackend(), react()],
  server: { port: 5199, strictPort: true, host: "127.0.0.1" },
});
