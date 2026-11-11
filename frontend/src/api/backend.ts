// 以 Wails 產生的 bindings 實作 Backend 介面. 前端只經由本檔案呼叫後端; 預覽頁的
// Vite 設定 (vite.preview.config.ts) 把本檔案換成以假資料實作的版本.
import { Browser, Clipboard, Events } from "@wailsio/runtime";
import { AppService, HostService, RecordingService, SettingsService } from "../../bindings/gpumon/cmd/gui/backend";
import type { Backend, BackendEvents } from "./types";

// 以 any 轉接 bindings 的列舉與類別型別; 兩者的 JSON 結構與 api/types.ts 相同.
const host = HostService as any;
const settings = SettingsService as any;
const recording = RecordingService as any;
const app = AppService as any;

// 後端以 JSON 解析參數, 帶小數的數值無法解析為整數型別而使呼叫失敗. 縮放, 平移與點選
// 曲線圖算出的時刻 (Unix 微秒) 與寬度可能帶小數, 送出前一律四捨五入.
const int = Math.round;

export const backend: Backend = {
  hosts: () => host.Hosts(),
  addHost: (conn) => host.AddHost(conn),
  updateHost: (id, conn) => host.UpdateHost(id, conn),
  removeHost: (id) => host.RemoveHost(id),
  connect: (id) => host.Connect(id),
  disconnect: (id) => host.Disconnect(id),
  answerPrompt: (id, secret) => host.AnswerPrompt(id, secret),
  cancelPrompt: (id) => host.CancelPrompt(id),
  confirmUpload: (id) => host.ConfirmUpload(id),
  liveAll: async () => (await host.LiveAll()) ?? [],
  live: (id) => host.Live(id),
  query: (id, from, to, width, fields, stats) => host.Query(id, int(from), int(to), int(width), fields, stats),
  chooseKeyFile: (title) => host.ChooseKeyFile(title),

  recordings: async () => {
    const l = await recording.List();
    return { hosts: l?.hosts ?? [], skipped: l?.skipped ?? [] };
  },
  deleteRecordings: async (paths) => (await recording.Delete(paths)) ?? [],
  recordingInfo: (path) => recording.Info(path),
  queryRecording: (path, from, to, width, fields) => recording.Query([path], int(from), int(to), int(width), fields),
  processesAt: async (path, t) => {
    const p = await recording.ProcessesAt(path, int(t));
    if (!p) {
      return null;
    }
    // 記錄內的程序以 omitempty 存放, 缺少的成員補上預設值.
    const procs = ((p.procs ?? []) as any[]).map((x) => ({
      gpu: x.gpu ?? 0,
      pid: x.pid ?? 0,
      user: x.user ?? "",
      mem: x.mem ?? null,
      util: x.util ?? null,
      cmd: x.cmd ?? "",
    }));
    return { time: p.time, procs };
  },

  getSettings: () => settings.Get(),
  choices: () => settings.Choices(),
  setAppearance: (v) => settings.SetAppearance(v),
  setLanguage: (v) => settings.SetLanguage(v),
  setMonitorSpan: (ms) => settings.SetMonitorSpan(ms),
  setHistorySpan: (ms, all) => settings.SetHistorySpan(ms, all),
  setCurves: (curves) => settings.SetCurves(curves),
  setGPUCollapsed: (hostID, gpuID, collapsed) => settings.SetGPUCollapsed(hostID, gpuID, collapsed),
  setProcessesCollapsed: (hostID, collapsed) => settings.SetProcessesCollapsed(hostID, collapsed),
  setProcessSort: (hostID, sort) => settings.SetProcessSort(hostID, sort),
  setProcessFilter: (hostID, filter) => settings.SetProcessFilter(hostID, filter),

  about: () => app.About(),
  confirmQuit: () => app.ConfirmQuit(),
  copyText: async (text) => {
    await Clipboard.SetText(text);
  },
  openURL: async (url) => {
    await Browser.OpenURL(url);
  },

  on<K extends keyof BackendEvents>(name: K, cb: (data: BackendEvents[K]) => void) {
    return Events.On(name, (ev: any) => cb(ev.data as BackendEvents[K]));
  },
};
