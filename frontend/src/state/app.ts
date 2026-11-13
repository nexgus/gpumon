// 全程式共用的狀態與操作. 狀態分為數個 store, 使新取樣只觸發讀數與曲線圖的更新.
// 密碼與金鑰密碼不經過本檔案的任何狀態, 見 dialogs/PromptDialog.tsx.
import { backend } from "../api/backend";
import { serviceError } from "../api/errors";
import type {
  Appearance,
  Choices,
  ConnectError,
  Curve,
  Field,
  HostConn,
  HostStatus,
  Language,
  Live,
  ProcFilter,
  ProcSort,
  Prompt,
  QuitRequest,
  ServiceError,
  SizeUnits,
  SettingsData,
  UploadNotice,
} from "../api/types";
import { setLanguage as applyLanguage } from "../i18n";
import { setAppearance as applyAppearance } from "../theme";
import { Store, useStore } from "./store";

export type Page = "monitor" | "recordings" | "history" | "settings";

export interface UIState {
  page: Page;
  // selected 為監看畫面顯示中的主機, 亦為側欄標示為選取的主機; 只會是有分頁的主機,
  // 沒有任何分頁時為 null.
  selected: string | null;
}

// MonitorTabsState 為決定監看畫面分頁範圍的額外狀態, 只存在於程式執行期間, 不存入設定檔.
// kept 為保留的分頁: 曾進入連線中而非因使用者按下 "中斷連線" 轉為未連線的主機, 直到使用者
// 關閉分頁或再次連線. leaving 為使用者已按下 "中斷連線" 而後端尚未回報未連線的主機, 其分頁
// 立即移除. opening 為使用者已按下 "連線" 而後端尚未回報正在連線的主機, 其分頁立即出現.
export interface MonitorTabsState {
  kept: ReadonlySet<string>;
  leaving: ReadonlySet<string>;
  opening: ReadonlySet<string>;
}

// OperationError 為操作失敗時顯示於錯誤視窗的一筆內容.
export interface OperationError {
  name: string;
  error: ServiceError;
}

// HistoryState 為歷史畫面開啟中的記錄檔與顯示範圍的位置. path 為開啟中記錄檔的路徑,
// null 表示沒有開啟中的記錄檔. left 為顯示範圍的左緣 (Unix 微秒), null 表示尚未決定,
// 由歷史畫面取捲動範圍的最右端. pinned 為固定時刻 (Unix 微秒), null 表示未固定. maxSel
// 為最大值卡片中選取的一格, null 表示未選取; 選取時 pinned 為其中一處的時刻, 清除或改設
// 固定時刻時一併解除. pinned 與 maxSel 皆不存入設定檔.
export interface HistoryState {
  path: string | null;
  left: number | null;
  pinned: number | null;
  maxSel: MaxSelection | null;
}

// MaxSelection 為最大值卡片中選取的一格: GPU 編號與欄位, 以及固定時刻所在的出現處
// (由 0 起算, 依時刻遞增).
export interface MaxSelection {
  gpu: number;
  field: Field;
  index: number;
}

export const hostsStore = new Store<HostStatus[]>([]);
export const liveStore = new Store<Record<string, Live>>({});
export const settingsStore = new Store<SettingsData | null>(null);
export const choicesStore = new Store<Choices | null>(null);
export const uiStore = new Store<UIState>({ page: "monitor", selected: null });
export const historyStore = new Store<HistoryState>({ path: null, left: null, pinned: null, maxSel: null });

export const monitorTabsStore = new Store<MonitorTabsState>({ kept: new Set(), leaving: new Set(), opening: new Set() });

// RecordingsListState 為記錄檔清單離開後仍保留的狀態, 只存在於程式執行期間, 不存入設定檔.
// selected 為各主機選取的檔案路徑. host 為目前所在的主機, 即第二層顯示其檔案清單者: 主機的識別
// 為 "h:" 加主機名稱, 無法開啟的檔案為 "s"; null 為第一層 (主機清單). scrollTop 為離開清單時
// 內容區的捲動位置.
export interface RecordingsListState {
  selected: ReadonlySet<string>;
  host: string | null;
  scrollTop: number;
}

export const recordingsListStore = new Store<RecordingsListState>({ selected: new Set(), host: null, scrollTop: 0 });
export const connectErrorsStore = new Store<ConnectError[]>([]);
export const operationErrorsStore = new Store<OperationError[]>([]);
// promptsStore 只存輸入要求的說明 (主機, 種類, 次數), 不含輸入的值.
export const promptsStore = new Store<Prompt[]>([]);
// uploadsStore 為等待使用者確認的上傳通知, 依收到的先後排列.
export const uploadsStore = new Store<UploadNotice[]>([]);
// quitStore 為等待使用者確認的結束要求; 再次要求結束時以新的主機數取代.
export const quitStore = new Store<QuitRequest | null>(null);

// 主區域離開歷史畫面即關閉開啟中的記錄檔, 一併清除其顯示位置與固定時刻.
uiStore.subscribe(() => {
  if (uiStore.get().page !== "history" && historyStore.get().path !== null) {
    historyStore.set({ path: null, left: null, pinned: null, maxSel: null });
  }
});

// hasTab 回傳主機在監看畫面是否有分頁: 正在連線, 連線中與重新連線中的主機, 以及保留的分頁;
// 使用者按下 "中斷連線" 後立即沒有分頁, 按下 "連線" 後立即有分頁.
export function hasTab(h: HostStatus, tabs: MonitorTabsState): boolean {
  if (tabs.leaving.has(h.id)) {
    return false;
  }
  return h.state !== "disconnected" || tabs.opening.has(h.id) || tabs.kept.has(h.id);
}

// tabHosts 回傳監看畫面有分頁的主機, 依側欄的主機順序.
export function tabHosts(hosts: HostStatus[], tabs: MonitorTabsState): HostStatus[] {
  return hosts.filter((h) => hasTab(h, tabs));
}

// 同時改動主機清單與分頁狀態時, 於全部改動完成後才調整顯示中的主機, 以免依中間狀態誤判分頁已移除.
let holdSync = false;

function atomically(fn: () => void): void {
  holdSync = true;
  try {
    fn();
  } finally {
    holdSync = false;
  }
  syncSelected();
}

// 監看畫面顯示中的主機必須有分頁; 其分頁被移除時改為剩餘分頁中的第一個, 沒有分頁時為 null.
function syncSelected(): void {
  if (holdSync) {
    return;
  }
  const ids = tabHosts(hostsStore.get(), monitorTabsStore.get()).map((h) => h.id);
  const u = uiStore.get();
  if (u.selected !== null && ids.includes(u.selected)) {
    return;
  }
  const next = ids[0] ?? null;
  if (next !== u.selected) {
    uiStore.set({ ...u, selected: next });
  }
}

hostsStore.subscribe(syncSelected);
monitorTabsStore.subscribe(syncSelected);

// updateTabs 以 fn 改動 monitorTabsStore 中 key 那一組主機識別.
function updateTabs(key: keyof MonitorTabsState, fn: (ids: Set<string>) => void): void {
  monitorTabsStore.update((s) => {
    const next = new Set(s[key]);
    fn(next);
    return next.size === s[key].size && [...next].every((id) => s[key].has(id)) ? s : { ...s, [key]: next };
  });
}

// 密碼輸入視窗, 上傳通知視窗, 連線錯誤視窗, 操作失敗視窗與結束確認視窗同一時間只
// 顯示一個. modalQueueStore 依出現的先後排列這些視窗, 只顯示第一個; 新出現的視窗排在
// 後面, 不遮蔽顯示中的視窗, 顯示中的視窗關閉後才顯示下一個. 鍵依序為 "prompt:<id>",
// "upload:<id>", "connect-errors", "operation-errors" 與 "quit".
export const modalQueueStore = new Store<string[]>([]);

function syncModalQueue(): void {
  const present = [
    ...promptsStore.get().map((p) => `prompt:${p.id}`),
    ...uploadsStore.get().map((u) => `upload:${u.id}`),
    ...(connectErrorsStore.get().length > 0 ? ["connect-errors"] : []),
    ...(operationErrorsStore.get().length > 0 ? ["operation-errors"] : []),
    ...(quitStore.get() ? ["quit"] : []),
  ];
  const old = modalQueueStore.get();
  const next = [...old.filter((k) => present.includes(k)), ...present.filter((k) => !old.includes(k))];
  if (next.length !== old.length || next.some((k, i) => k !== old[i])) {
    modalQueueStore.set(next);
  }
}

promptsStore.subscribe(syncModalQueue);
uploadsStore.subscribe(syncModalQueue);
connectErrorsStore.subscribe(syncModalQueue);
operationErrorsStore.subscribe(syncModalQueue);
quitStore.subscribe(syncModalQueue);

export const defaultCurves: Curve[] = ["util", "mem"];

function reportOperation(name: string, err: unknown): void {
  const error = serviceError(err);
  operationErrorsStore.update((list) => [...list, { name, error }]);
}

function replaceHost(id: string, next: HostStatus): void {
  hostsStore.update((list) => list.map((h) => (h.id === id ? next : h)));
}

async function refreshHosts(): Promise<void> {
  hostsStore.set(await backend.hosts());
}

async function refreshSettings(): Promise<void> {
  settingsStore.set(await backend.getSettings());
}

function onHostEvent(st: HostStatus): void {
  const list = hostsStore.get();
  const old = list.find((h) => h.id === st.id);
  if (!old) {
    void refreshHosts();
    return;
  }
  if (old.state === "disconnected" && st.state === "connecting") {
    // 後端於開始連線時清除前一次連線的即時資料, 此處同步清除.
    liveStore.update((m) => ({ ...m, [st.id]: { ...emptyLive(st.id), devices: m[st.id]?.devices ?? null } }));
  }
  // 轉為未連線時, 使用者按下 "中斷連線" 所致者移除分頁; 其餘曾進入連線中者 (重試用盡或重新連線
  // 時發生不可重試的錯誤) 保留分頁. 首次連線失敗 (由正在連線轉為未連線) 不保留.
  atomically(() => {
    if (st.state === "disconnected") {
      const byUser = monitorTabsStore.get().leaving.has(st.id);
      updateTabs("leaving", (ids) => ids.delete(st.id));
      updateTabs("opening", (ids) => ids.delete(st.id));
      if (!byUser && (old.state === "connected" || old.state === "reconnecting")) {
        updateTabs("kept", (ids) => ids.add(st.id));
      }
    } else {
      updateTabs("opening", (ids) => ids.delete(st.id));
      updateTabs("kept", (ids) => ids.delete(st.id));
    }
    replaceHost(st.id, st);
  });
}

export function emptyLive(id: string): Live {
  return { host: id, devices: null, conn_start: 0, has_sample: false, time: 0, interval_ms: 0, gpus: [], procs: [] };
}

// init 讀取設定, 主機清單與即時資料, 並註冊後端事件. 回傳的函數取消註冊.
export async function init(): Promise<() => void> {
  const offs = [
    backend.on("monitor:host", onHostEvent),
    backend.on("monitor:sample", (lv) => liveStore.update((m) => ({ ...m, [lv.host]: lv }))),
    backend.on("monitor:devices", (ev) =>
      liveStore.update((m) => ({ ...m, [ev.host]: { ...(m[ev.host] ?? emptyLive(ev.host)), devices: ev.devices } })),
    ),
    backend.on("monitor:error", (ce) =>
      connectErrorsStore.update((list) => [...list.filter((e) => e.host !== ce.host), ce]),
    ),
    backend.on("monitor:prompt", (p) => promptsStore.update((list) => [...list, p])),
    backend.on("monitor:prompt-closed", (c) => promptsStore.update((list) => list.filter((p) => p.id !== c.id))),
    backend.on("monitor:upload", (n) => uploadsStore.update((list) => [...list, n])),
    backend.on("monitor:upload-closed", (c) => uploadsStore.update((list) => list.filter((n) => n.id !== c.id))),
    backend.on("app:quit-request", (q) => quitStore.set(q)),
    backend.on("app:show-page", (p) => showPage(p.page)),
  ];
  const [settings, choices, hosts, lives] = await Promise.all([
    backend.getSettings(),
    backend.choices(),
    backend.hosts(),
    backend.liveAll(),
  ]);
  applyAppearance(settings.appearance);
  applyLanguage(settings.language);
  settingsStore.set(settings);
  choicesStore.set(choices);
  // 後端只對重試用盡而轉為未連線的主機保留斷線原因; 讀取主機清單時這些主機的分頁即為保留.
  atomically(() => {
    updateTabs("kept", (ids) => hosts.filter((h) => h.state === "disconnected" && h.reason !== "").forEach((h) => ids.add(h.id)));
    hostsStore.set(hosts);
  });
  const live: Record<string, Live> = {};
  for (const lv of lives) {
    live[lv.host] = lv;
  }
  liveStore.set(live);
  return () => offs.forEach((off) => off());
}

function patchSettings(patch: Partial<SettingsData>): void {
  const s = settingsStore.get();
  if (s) {
    settingsStore.set({ ...s, ...patch });
  }
}

// 以下為設定的變更. 先更新畫面再寫入後端; 寫入失敗時還原並顯示錯誤.

export async function setAppearance(v: Appearance): Promise<void> {
  const old = settingsStore.get()?.appearance;
  patchSettings({ appearance: v });
  applyAppearance(v);
  try {
    await backend.setAppearance(v);
  } catch (err) {
    if (old) {
      patchSettings({ appearance: old });
      applyAppearance(old);
    }
    reportOperation("", err);
  }
}

export async function setLanguage(v: Language): Promise<void> {
  const old = settingsStore.get()?.language;
  patchSettings({ language: v });
  applyLanguage(v);
  try {
    await backend.setLanguage(v);
  } catch (err) {
    if (old) {
      patchSettings({ language: old });
      applyLanguage(old);
    }
    reportOperation("", err);
  }
}

// setSizeUnits 設定容量單位制, 寫入失敗時還原.
export async function setSizeUnits(v: SizeUnits): Promise<void> {
  const old = settingsStore.get()?.size_units;
  patchSettings({ size_units: v });
  try {
    await backend.setSizeUnits(v);
  } catch (err) {
    if (old) {
      patchSettings({ size_units: old });
    }
    reportOperation("", err);
  }
}

// useSizeUnits 回傳目前的容量單位制; 設定變更時使用它的元件重新繪製.
export function useSizeUnits(): SizeUnits {
  return useStore(settingsStore, (s) => s?.size_units) ?? "binary";
}

async function saveSetting<K extends keyof SettingsData>(
  key: K,
  v: SettingsData[K],
  save: () => Promise<void>,
): Promise<void> {
  const old = settingsStore.get()?.[key];
  patchSettings({ [key]: v } as Partial<SettingsData>);
  try {
    await save();
  } catch (err) {
    patchSettings({ [key]: old } as Partial<SettingsData>);
    reportOperation("", err);
  }
}

// 顯示範圍長度於縮放 (例如捏合) 過程中連續改變, 畫面立即更新, 寫入設定檔則於停止改變
// spanSaveDelay 毫秒後進行一次. 寫入失敗時顯示錯誤, 畫面上的長度維持不變.
const spanSaveDelay = 400;
let monitorSpanTimer: number | undefined;
let historySpanTimer: number | undefined;

// setMonitorSpan 變更監看畫面的顯示範圍長度 (毫秒), 所有主機共用.
export function setMonitorSpan(ms: number): void {
  const v = Math.round(ms);
  if (settingsStore.get()?.monitor_span_ms === v) {
    return;
  }
  patchSettings({ monitor_span_ms: v });
  window.clearTimeout(monitorSpanTimer);
  monitorSpanTimer = window.setTimeout(() => {
    const s = settingsStore.get();
    if (s) {
      backend.setMonitorSpan(s.monitor_span_ms).catch((err) => reportOperation("", err));
    }
  }, spanSaveDelay);
}

// setHistorySpan 變更歷史畫面的顯示範圍長度 (毫秒, 設定檔中不短於 minMS) 與是否處於顯示
// 全部狀態.
export function setHistorySpan(ms: number, all: boolean, minMS: number): void {
  const v = Math.max(minMS, Math.round(ms));
  const s = settingsStore.get();
  if (s?.history_span_ms === v && s.history_all === all) {
    return;
  }
  patchSettings({ history_span_ms: v, history_all: all });
  window.clearTimeout(historySpanTimer);
  historySpanTimer = window.setTimeout(() => {
    const cur = settingsStore.get();
    if (cur) {
      backend.setHistorySpan(cur.history_span_ms, cur.history_all).catch((err) => reportOperation("", err));
    }
  }, spanSaveDelay);
}

export function setCurves(v: Curve[]): Promise<void> {
  return saveSetting("curves", v, () => backend.setCurves(v));
}

export async function setGPUCollapsed(hostID: string, gpuID: number, collapsed: boolean): Promise<void> {
  const s = settingsStore.get();
  const all = { ...(s?.collapsed_gpus ?? {}) };
  const ids = new Set(all[hostID] ?? []);
  if (collapsed) {
    ids.add(gpuID);
  } else {
    ids.delete(gpuID);
  }
  all[hostID] = [...ids].sort((a, b) => a - b);
  await saveSetting("collapsed_gpus", all, () => backend.setGPUCollapsed(hostID, gpuID, collapsed));
}

// setProcessesCollapsed 記住一台主機的程序卡片是否收合; 監看畫面與歷史畫面共用.
export async function setProcessesCollapsed(hostID: string, collapsed: boolean): Promise<void> {
  const ids = new Set(settingsStore.get()?.collapsed_procs ?? []);
  if (collapsed) {
    ids.add(hostID);
  } else {
    ids.delete(hostID);
  }
  await saveSetting("collapsed_procs", [...ids].sort(), () => backend.setProcessesCollapsed(hostID, collapsed));
}

// setMaximaCollapsed 記住一台主機在歷史畫面的最大值卡片是否收合.
export async function setMaximaCollapsed(hostID: string, collapsed: boolean): Promise<void> {
  const ids = new Set(settingsStore.get()?.collapsed_maxima ?? []);
  if (collapsed) {
    ids.add(hostID);
  } else {
    ids.delete(hostID);
  }
  await saveSetting("collapsed_maxima", [...ids].sort(), () => backend.setMaximaCollapsed(hostID, collapsed));
}

// defaultProcSort 為程序卡片的預設排序方式, 依 GPU 遞增.
export const defaultProcSort: ProcSort = { column: "gpu", order: "asc" };

// normalizeProcFilter 去除文字條件的前後空白, GPU 編號去除重複並由小到大排列.
export function normalizeProcFilter(f: ProcFilter): ProcFilter {
  return {
    gpus: [...new Set(f.gpus ?? [])].sort((a, b) => a - b),
    user: f.user.trim(),
    command: f.command.trim(),
  };
}

// procFilterEmpty 回傳過濾條件是否沒有任何條件.
export function procFilterEmpty(f: ProcFilter | undefined): boolean {
  return !f || ((f.gpus?.length ?? 0) === 0 && f.user === "" && f.command === "");
}

// setProcessSort 記住一台主機的程序卡片排序方式; 監看畫面與歷史畫面共用.
export async function setProcessSort(hostID: string, sort: ProcSort): Promise<void> {
  const all = { ...(settingsStore.get()?.proc_sorts ?? {}) };
  if (sort.column === defaultProcSort.column && sort.order === defaultProcSort.order) {
    delete all[hostID];
  } else {
    all[hostID] = sort;
  }
  await saveSetting("proc_sorts", all, () => backend.setProcessSort(hostID, sort));
}

// setProcessFilter 記住一台主機的程序卡片過濾條件; 監看畫面與歷史畫面共用. 沒有任何
// 條件時清除該主機的記錄.
export async function setProcessFilter(hostID: string, filter: ProcFilter): Promise<void> {
  const f = normalizeProcFilter(filter);
  const all = { ...(settingsStore.get()?.proc_filters ?? {}) };
  if (procFilterEmpty(f)) {
    delete all[hostID];
  } else {
    all[hostID] = f;
  }
  await saveSetting("proc_filters", all, () => backend.setProcessFilter(hostID, f));
}

// PinnedProc 為程序卡片中一列置頂的程序, 以 GPU 編號, PID 與指令識別. user 為置頂期間
// 最後一次取得的使用者名稱.
export interface PinnedProc {
  gpu: number;
  pid: number;
  cmd: string;
  user: string;
}

// pinsStore 為各主機置頂的程序, 鍵為主機識別字串, 依置頂的先後排列. 只存在於程式執行
// 期間, 不存入設定檔; 監看畫面與歷史畫面共用.
export const pinsStore = new Store<Record<string, PinnedProc[]>>({});

// samePinned 回傳 a 與 b 是否為同一列: GPU 編號, PID 與指令皆相同.
export function samePinned(a: { gpu: number; pid: number; cmd: string }, b: { gpu: number; pid: number; cmd: string }): boolean {
  return a.gpu === b.gpu && a.pid === b.pid && a.cmd === b.cmd;
}

// setPinned 把一台主機的一列程序置頂或解除置頂.
export function setPinned(hostID: string, p: PinnedProc, pinned: boolean): void {
  pinsStore.update((all) => {
    const list = all[hostID] ?? [];
    const rest = list.filter((x) => !samePinned(x, p));
    const next = pinned ? [...rest, { gpu: p.gpu, pid: p.pid, cmd: p.cmd, user: p.user }] : rest;
    if (next.length === list.length && !pinned) {
      return all;
    }
    const out = { ...all };
    if (next.length > 0) {
      out[hostID] = next;
    } else {
      delete out[hostID];
    }
    return out;
  });
}

// refreshPinnedUsers 以 procs 中取得的使用者名稱更新該主機置頂的列; 沒有改變時不更新.
export function refreshPinnedUsers(hostID: string, procs: { gpu: number; pid: number; cmd: string; user: string }[]): void {
  const list = pinsStore.get()[hostID];
  if (!list) {
    return;
  }
  let changed = false;
  const next = list.map((x) => {
    const p = procs.find((q) => samePinned(q, x));
    if (p && p.user !== x.user) {
      changed = true;
      return { ...x, user: p.user };
    }
    return x;
  });
  if (changed) {
    pinsStore.update((all) => ({ ...all, [hostID]: next }));
  }
}

// movePins 把置頂由主機識別 from 轉移至 to; to 為 null 時清除.
function movePins(from: string, to: string | null): void {
  const all = pinsStore.get();
  if (!(from in all)) {
    return;
  }
  const out = { ...all };
  if (to !== null) {
    out[to] = all[from];
  }
  delete out[from];
  pinsStore.set(out);
}

// 以下為主機的操作.

export async function addHost(conn: HostConn): Promise<HostStatus> {
  const st = await backend.addHost(conn);
  hostsStore.update((list) => [...list, st]);
  return st;
}

export async function updateHost(id: string, conn: HostConn): Promise<HostStatus> {
  const st = await backend.updateHost(id, conn);
  atomically(() => {
    if (st.id !== id && monitorTabsStore.get().kept.has(id)) {
      updateTabs("kept", (ids) => {
        ids.delete(id);
        ids.add(st.id);
      });
    }
    replaceHost(id, st);
    uiStore.update((u) => ({ ...u, selected: u.selected === id ? st.id : u.selected }));
  });
  if (st.id !== id) {
    liveStore.update((m) => {
      const next = { ...m };
      delete next[id];
      return next;
    });
    // 收合狀態與程序卡片的排序方式及過濾條件由後端隨主機的識別轉移, 重新讀取設定以
    // 取得新的鍵. 置頂不存入設定檔, 於此轉移.
    movePins(id, st.id);
    void refreshSettings();
  }
  return st;
}

export async function removeHost(id: string): Promise<void> {
  try {
    await backend.removeHost(id);
  } catch (err) {
    reportOperation(hostsStore.get().find((h) => h.id === id)?.name ?? id, err);
    return;
  }
  atomically(() => {
    hostsStore.update((list) => list.filter((h) => h.id !== id));
    updateTabs("kept", (ids) => ids.delete(id));
  });
  liveStore.update((m) => {
    const next = { ...m };
    delete next[id];
    return next;
  });
  movePins(id, null);
  void refreshSettings();
}

// connect 為側欄 "連線" 的行為: 立即為該主機建立分頁 (保留的分頁轉為一般分頁), 並於主區域
// 顯示監看畫面與該分頁, 再要求後端連線. 要求失敗時移除該分頁.
export async function connect(id: string): Promise<void> {
  atomically(() => {
    updateTabs("leaving", (ids) => ids.delete(id));
    updateTabs("kept", (ids) => ids.delete(id));
    updateTabs("opening", (ids) => ids.add(id));
  });
  uiStore.update((u) => ({ ...u, page: "monitor", selected: id }));
  try {
    await backend.connect(id);
  } catch (err) {
    updateTabs("opening", (ids) => ids.delete(id));
    reportOperation(hostsStore.get().find((h) => h.id === id)?.name ?? id, err);
  }
}

// disconnect 為側欄 "中斷連線" 的行為: 立即移除該主機的分頁, 再要求後端中斷連線. 要求失敗時
// 恢復該分頁.
export async function disconnect(id: string): Promise<void> {
  updateTabs("kept", (ids) => ids.delete(id));
  updateTabs("leaving", (ids) => ids.add(id));
  try {
    await backend.disconnect(id);
  } catch (err) {
    updateTabs("leaving", (ids) => ids.delete(id));
    reportOperation(hostsStore.get().find((h) => h.id === id)?.name ?? id, err);
    return;
  }
  // 後端已回報未連線時 leaving 已清除; 主機原本即未連線而不會再有回報時於此清除.
  if (hostsStore.get().find((h) => h.id === id)?.state === "disconnected") {
    updateTabs("leaving", (ids) => ids.delete(id));
  }
}

// closeTab 關閉保留的分頁.
export function closeTab(id: string): void {
  updateTabs("kept", (ids) => ids.delete(id));
}

// 以下為畫面的切換.

export function showPage(page: Page): void {
  uiStore.update((u) => ({ ...u, page }));
}

// selectHost 於主區域顯示監看畫面並切換至該主機的分頁; 該主機沒有分頁時不做任何改變.
export function selectHost(id: string): void {
  const h = hostsStore.get().find((x) => x.id === id);
  if (!h || !hasTab(h, monitorTabsStore.get())) {
    return;
  }
  uiStore.update((u) => ({ ...u, page: "monitor", selected: id }));
}

// showRecordings 為側欄 "記錄檔" 的行為: 顯示記錄檔清單的第一層 (主機清單).
export function showRecordings(): void {
  recordingsListStore.update((r) => (r.host === null ? r : { ...r, host: null, scrollTop: 0 }));
  showPage("recordings");
}

// openRecording 開啟 path 的記錄檔並顯示歷史畫面.
export function openRecording(path: string): void {
  historyStore.set({ path, left: null, pinned: null, maxSel: null });
  showPage("history");
}

export function setHistoryLeft(left: number): void {
  historyStore.update((h) => ({ ...h, left }));
}

// setHistoryPinned 設定歷史畫面的固定時刻, null 為清除. 最大值卡片的選取隨之解除.
export function setHistoryPinned(pinned: number | null): void {
  historyStore.update((h) => (h.pinned === pinned && h.maxSel === null ? h : { ...h, pinned, maxSel: null }));
}

// selectHistoryMax 選取最大值卡片的一格, 並把固定時刻設為所選出現處的時刻 t.
export function selectHistoryMax(sel: MaxSelection, t: number): void {
  historyStore.update((h) => ({ ...h, pinned: t, maxSel: sel }));
}

// reportOperationFailure 把某項操作失敗的原因顯示於錯誤視窗.
export function reportOperationFailure(name: string, error: ServiceError): void {
  operationErrorsStore.update((list) => [...list, { name, error }]);
}

// confirmUpload 送出上傳通知的確認並關閉該通知視窗.
export function confirmUpload(id: string): void {
  uploadsStore.update((list) => list.filter((n) => n.id !== id));
  void backend.confirmUpload(id).catch(() => undefined);
}

export function dismissConnectErrors(): void {
  connectErrorsStore.set([]);
}

export function dismissOperationErrors(): void {
  operationErrorsStore.set([]);
}

// confirmQuit 為結束確認視窗的 "結束": 關閉視窗並結束程式, 所有連線隨之中斷.
export function confirmQuit(): void {
  quitStore.set(null);
  void backend.confirmQuit().catch(() => undefined);
}

export function cancelQuit(): void {
  quitStore.set(null);
}
