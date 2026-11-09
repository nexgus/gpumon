import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { loadFonts } from "./fonts";
import { init } from "./state/app";
import "./styles/theme.css";
import "./styles/app.css";

loadFonts();

const root = createRoot(document.getElementById("root") as HTMLElement);
const params = new URLSearchParams(location.search);

// 主視窗與關於視窗共用同一個頁面, 以網址參數 window=about 區分. 關於視窗的程式與
// 第三方授權清單另行載入, 不增加主視窗的載入量.
if (params.get("window") === "about") {
  void import("./about/start").then(({ startAbout }) =>
    startAbout(root, params.get("tab") === "licenses" ? "licenses" : "about"),
  );
} else {
  init().finally(() => {
    root.render(
      <StrictMode>
        <App />
      </StrictMode>,
    );
  });
}
