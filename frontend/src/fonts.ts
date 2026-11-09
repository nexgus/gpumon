// 內建字型 IBM Plex Sans 與 IBM Plex Mono, 執行期不自網路載入. 以 JS 匯入字型檔的網址,
// 使打包套件清單 (見 vite.config.ts) 記錄此二套件, 列入第三方授權清單.
import sans400 from "@fontsource/ibm-plex-sans/files/ibm-plex-sans-latin-400-normal.woff2?url";
import sans500 from "@fontsource/ibm-plex-sans/files/ibm-plex-sans-latin-500-normal.woff2?url";
import sans600 from "@fontsource/ibm-plex-sans/files/ibm-plex-sans-latin-600-normal.woff2?url";
import mono400 from "@fontsource/ibm-plex-mono/files/ibm-plex-mono-latin-400-normal.woff2?url";
import mono500 from "@fontsource/ibm-plex-mono/files/ibm-plex-mono-latin-500-normal.woff2?url";
import mono600 from "@fontsource/ibm-plex-mono/files/ibm-plex-mono-latin-600-normal.woff2?url";

// latin 子集的字元範圍, 與 @fontsource 的 latin.css 相同; 範圍外的字元 (例如中文)
// 改用系統字型.
const latinRange =
  "U+0000-00FF, U+0131, U+0152-0153, U+02BB-02BC, U+02C6, U+02DA, U+02DC, U+0304, U+0308, U+0329, " +
  "U+2000-206F, U+20AC, U+2122, U+2191, U+2193, U+2212, U+2215, U+FEFF, U+FFFD";

const faces: [string, string, string][] = [
  ["IBM Plex Sans", sans400, "400"],
  ["IBM Plex Sans", sans500, "500"],
  ["IBM Plex Sans", sans600, "600"],
  ["IBM Plex Mono", mono400, "400"],
  ["IBM Plex Mono", mono500, "500"],
  ["IBM Plex Mono", mono600, "600"],
];

export function loadFonts(): void {
  for (const [family, url, weight] of faces) {
    const face = new FontFace(family, `url(${url}) format("woff2")`, {
      weight,
      style: "normal",
      display: "swap",
      unicodeRange: latinRange,
    });
    document.fonts.add(face);
    face.load().catch(() => undefined);
  }
}
