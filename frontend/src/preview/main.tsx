// 預覽頁的進入點, 以假後端 (preview/mockBackend.ts) 驅動正式的元件, 供視覺核對.
// 以 npx vite --config vite.preview.config.ts 啟動, 開啟 /preview.html. 網址參數:
//   theme=light|dark, lang=en|zh-TW, range=<監看畫面的顯示範圍>, curves=util,mem,...,
//   collapse=1 (收合 GPU 2), pcollapse=1 (收合第一台主機的程序卡片), host=<n> (切換至第 n 個
//   主機分頁, 自 0 起), scenario=design|empty|nogpu, live=0 (不送新取樣),
//   now=<Unix 秒> (固定目前時刻), dialog=add|edit|delete|password|passphrase|errors|upload|update|queue
//   (queue 依序送出上傳通知, 錯誤與密碼輸入要求, 只顯示第一個; next=<n> 依序按下前 n 個視窗的主要按鍵),
//   hover=<0 至 1> (於第 n 張曲線圖的該位置懸停), hoverChart=<n>, pin=<0 至 1> (於第 n 張曲線圖
//   的該位置點選以固定時刻, n 為 hoverChart),
//   page=recordings|history|settings (顯示該畫面), hrange=<歷史畫面的顯示範圍>,
//   select=all|<記錄檔清單由上而下的列號, 以逗號分隔, 自 0 起> (勾選), openrec=1 (按 Open,
//   顯示歷史畫面), confirmdelete=1 (按 Delete, 顯示確認視窗), goto=<YYYY-MM-DD HH:MM:SS>
//   (歷史畫面跳至該時刻), dialog=quit (結束確認視窗, count=<主機數>, 預設 2),
//   window=about (關於視窗, 以 600 x 600 的視窗檢視; tab=about|licenses,
//   platform=windows 顯示 Windows 的資訊, jump=<n> 點選第三方授權分頁表中第 n 個授權種類).
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "../App";
import { startAbout } from "../about/start";
import { loadFonts } from "../fonts";
import { init } from "../state/app";
import { mock } from "./mockBackend";
import "../styles/theme.css";
import "../styles/app.css";

const params = new URLSearchParams(location.search);

function click(selector: string, index = 0): void {
  const el = document.querySelectorAll<HTMLElement>(selector)[index];
  el?.click();
}

function later(fn: () => void, ms = 300): void {
  window.setTimeout(fn, ms);
}

function setInput(selector: string, value: string): void {
  const el = document.querySelector<HTMLInputElement>(selector);
  if (!el) {
    return;
  }
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
  setter?.call(el, value);
  el.dispatchEvent(new Event("input", { bubbles: true }));
}

function uploadNotice(id: string, host: string, update: boolean) {
  return {
    id,
    host,
    name: host,
    path: "~/.gpumon/gpumon",
    update,
    remote_version: update ? "0.1.0" : "",
    new_version: update ? "0.2.0-beta.1" : "0.1.0",
  };
}

function scenario(): void {
  switch (params.get("dialog")) {
    case "add":
      click(".side-add button");
      later(() => {
        setInput("#hf-addr", "10.90.20.55");
        setInput("#hf-user", "sweetener");
      }, 50);
      break;
    case "edit":
      click('.side-host-actions button[aria-label^="Edit"]', 2);
      later(() => {
        setInput("#hf-key", "~/.ssh/lab_ed25519");
        setInput("#hf-user", "");
        later(() => click('.dialog button[type="submit"]'), 50);
      }, 50);
      break;
    case "delete":
      click('.side-host-actions button[aria-label^="Delete"]', 2);
      break;
    case "password":
      mock.emit("monitor:prompt", {
        id: "p1",
        host: "sweetener@10.90.20.55",
        name: "sweetener@10.90.20.55",
        kind: "password",
        user: "sweetener",
        server: "10.90.20.55",
        key_file: "",
        attempt: Number(params.get("attempt") ?? 1),
      });
      break;
    case "passphrase":
      mock.emit("monitor:prompt", {
        id: "p2",
        host: "alice@gpu-lab-02",
        name: "alice@gpu-lab-02",
        kind: "passphrase",
        user: "",
        server: "",
        key_file: "~/.ssh/lab_ed25519",
        attempt: 1,
      });
      break;
    case "upload":
    case "update":
      mock.emit("monitor:upload", uploadNotice("u1", "sweetener@10.90.20.55", params.get("dialog") === "update"));
      break;
    case "queue":
      mock.emit("monitor:upload", uploadNotice("u2", "sweetener@10.90.20.55", false));
      mock.emit("monitor:upload", uploadNotice("u3", "alice@gpu-lab-02", true));
      mock.emit("monitor:error", {
        host: "bob@render-farm",
        name: "bob@render-farm",
        kind: "platform",
        message: "This host is Darwin arm64. GPU Monitor supports Linux x86_64 hosts only.",
      });
      mock.emit("monitor:prompt", {
        id: "p3",
        host: "carol@10.0.8.21",
        name: "carol@10.0.8.21",
        kind: "password",
        user: "carol",
        server: "10.0.8.21",
        key_file: "",
        attempt: 1,
      });
      for (let i = 0; i < Number(params.get("next") ?? 0); i++) {
        later(() => click('.dialog button[type="submit"]'), 100 * (i + 1));
      }
      break;
    case "quit":
      mock.emit("app:quit-request", { count: Number(params.get("count") ?? 2) });
      break;
    case "errors":
      mock.emit("monitor:error", {
        host: "bob@render-farm",
        name: "bob@render-farm",
        kind: "platform",
        message: "This host is Darwin arm64. GPU Monitor supports Linux x86_64 hosts only.",
      });
      later(() =>
        mock.emit("monitor:error", {
          host: "carol@10.0.8.21",
          name: "carol@10.0.8.21",
          kind: "protocol-too-new",
          message: "The remote program on 10.0.8.21 uses protocol 2, newer than protocol 1 of this app",
        }),
      );
      later(
        () =>
          mock.emit("monitor:error", {
            host: "dave@10.0.8.30",
            name: "dave@10.0.8.30",
            kind: "host-key",
            message: "The host key of 10.0.8.30 does not match ~/.ssh/known_hosts",
          }),
        600,
      );
      break;
  }
  const host = params.get("host");
  if (host !== null) {
    click(".host-tab", Number(host));
  }
  const page = params.get("page");
  if (page === "settings") {
    click(".side-nav-item", 1);
  }
  if (page === "recordings" || page === "history") {
    click(".side-nav-item", 0);
    later(() => {
      const select = params.get("select");
      if (select === "all") {
        click(".toolbar .btn:not(.btn-primary)");
      } else if (select) {
        for (const i of select.split(",")) {
          click(".rec-row input", Number(i));
        }
      }
      later(() => {
        if (params.get("confirmdelete") === "1") {
          click(".toolbar .btn:last-child");
        }
        if (page === "history" || params.get("openrec") === "1") {
          click(".toolbar .btn-primary");
        }
        const goto = params.get("goto");
        if (goto) {
          later(() => {
            setInput(".jump-input", goto);
            click('.toolbar form button[type="submit"]');
          }, 500);
        }
      }, 100);
    }, 300);
  }
  const pin = params.get("pin");
  if (pin !== null) {
    later(() => {
      const plot = document.querySelectorAll<HTMLElement>(".chart-plot")[Number(params.get("hoverChart") ?? 0)];
      if (!plot) {
        return;
      }
      const r = plot.getBoundingClientRect();
      plot.dispatchEvent(
        new MouseEvent("click", { bubbles: true, clientX: r.left + r.width * Number(pin), clientY: r.top + r.height / 2 }),
      );
    }, page === "history" ? 2000 : 800);
  }
  const hover = params.get("hover");
  if (hover !== null) {
    later(() => {
      const plot = document.querySelectorAll<HTMLElement>(".chart-plot")[Number(params.get("hoverChart") ?? 0)];
      if (!plot) {
        return;
      }
      const r = plot.getBoundingClientRect();
      plot.dispatchEvent(
        new MouseEvent("mousemove", { bubbles: true, clientX: r.left + r.width * Number(hover), clientY: r.top + r.height / 2 }),
      );
    }, page === "history" ? 2200 : 800);
  }
}

loadFonts();
const root = createRoot(document.getElementById("root") as HTMLElement);
if (params.get("window") === "about") {
  void startAbout(root, params.get("tab") === "licenses" ? "licenses" : "about").then(() => {
    const jump = params.get("jump");
    if (jump !== null) {
      later(() => click(".license-kind a", Number(jump)));
    }
  });
} else {
  init().finally(() => {
    root.render(
      <StrictMode>
        <App />
      </StrictMode>,
    );
    later(scenario, 400);
  });
}
