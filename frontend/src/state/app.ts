// 全程式共用的狀態與操作. 狀態分為數個 store, 使新取樣只觸發讀數與曲線圖的更新.
// 密碼與金鑰密碼不經過本檔案的任何狀態, 見 dialogs/PromptDialog.tsx.
import { backend } from "../api/backend";
import { serviceError } from "../api/errors";
import type {
  Appearance,
  Choices,
  ConnectError,
  Curve,
  HostConn,
  HostStatus,
  Language,
  Live,
  Prompt,
  QuitRequest,
  ServiceError,
  SettingsData,
  TimeRange,
  UploadNotice,
  ViewMode,
} from "../api/types";
import { setLanguage as applyLanguage } from "../i18n";
import { setAppearance as applyAppearance } from "../theme";
import { Store } from "./store";

export type Page = "monitor" | "recordings" | "history" | "settings";

export interface UIState {
  page: Page;
  // selected 為分頁顯示時顯示中的主機, 亦為側欄標示為選取的主機.
  selected: string | null;
  // scrollTo 為同時顯示時要捲動至的主機; seq 使重複點選同一台主機時仍會捲動.
  scrollTo: { id: string; seq: number } | null;
  // collapsedHosts 為同時顯示時收合的主機區塊, 只記於本次執行期間.
  collapsedHosts: Record<string, boolean>;
}

// OperationError 為操作失敗時顯示於錯誤視窗的一筆內容.
export interface OperationError {
  name: string;
  error: ServiceError;
}

// HistoryState 為歷史畫面開啟中的記錄檔與顯示範圍的位置. left 為顯示範圍的左緣
// (Unix 微秒), null 表示尚未決定, 由歷史畫面取捲動範圍的最右端.
export interface HistoryState {
  paths: string[];
  left: number | null;
}

export const hostsStore = new Store<HostStatus[]>([]);
export const liveStore = new Store<Record<string, Live>>({});
export const settingsStore = new Store<SettingsData | null>(null);
export const choicesStore = new Store<Choices | null>(null);
export const uiStore = new Store<UIState>({ page: "monitor", selected: null, scrollTo: null, collapsedHosts: {} });
export const historyStore = new Store<HistoryState>({ paths: [], left: null });
export const connectErrorsStore = new Store<ConnectError[]>([]);
export const operationErrorsStore = new Store<OperationError[]>([]);
// promptsStore 只存輸入要求的說明 (主機, 種類, 次數), 不含輸入的值.
export const promptsStore = new Store<Prompt[]>([]);
// uploadsStore 為等待使用者確認的上傳通知, 依收到的先後排列.
export const uploadsStore = new Store<UploadNotice[]>([]);
// quitStore 為等待使用者確認的結束要求; 再次要求結束時以新的主機數取代.
export const quitStore = new Store<QuitRequest | null>(null);

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

function patchHost(id: string, patch: Partial<HostStatus>): void {
  hostsStore.update((list) => list.map((h) => (h.id === id ? { ...h, ...patch } : h)));
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
  replaceHost(st.id, st);
}

export function emptyLive(id: string): Live {
  return { host: id, devices: null, has_sample: false, time: 0, interval_ms: 0, gpus: [], procs: [] };
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
  hostsStore.set(hosts);
  const live: Record<string, Live> = {};
  for (const lv of lives) {
    live[lv.host] = lv;
  }
  liveStore.set(live);
  uiStore.update((u) => ({ ...u, selected: hosts[0]?.id ?? null }));
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

export function setViewMode(v: ViewMode): Promise<void> {
  return saveSetting("view_mode", v, () => backend.setViewMode(v));
}

export function setSharedRange(v: TimeRange): Promise<void> {
  return saveSetting("shared_range", v, () => backend.setSharedRange(v));
}

export function setHistoryRange(v: TimeRange): Promise<void> {
  return saveSetting("history_range", v, () => backend.setHistoryRange(v));
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

// 以下為主機的操作.

export async function addHost(conn: HostConn): Promise<HostStatus> {
  const st = await backend.addHost(conn);
  hostsStore.update((list) => [...list, st]);
  return st;
}

export async function updateHost(id: string, conn: HostConn): Promise<HostStatus> {
  const st = await backend.updateHost(id, conn);
  replaceHost(id, st);
  if (st.id !== id) {
    liveStore.update((m) => {
      const next = { ...m };
      delete next[id];
      return next;
    });
    uiStore.update((u) => ({ ...u, selected: u.selected === id ? st.id : u.selected }));
    // 收合狀態由後端隨主機的識別轉移, 重新讀取設定以取得新的鍵.
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
  const list = hostsStore.get();
  const at = list.findIndex((h) => h.id === id);
  const rest = list.filter((h) => h.id !== id);
  hostsStore.set(rest);
  liveStore.update((m) => {
    const next = { ...m };
    delete next[id];
    return next;
  });
  uiStore.update((u) => ({
    ...u,
    selected: u.selected === id ? (rest[Math.min(at, rest.length - 1)]?.id ?? null) : u.selected,
  }));
  void refreshSettings();
}

export async function connect(id: string): Promise<void> {
  try {
    await backend.connect(id);
  } catch (err) {
    reportOperation(hostsStore.get().find((h) => h.id === id)?.name ?? id, err);
  }
}

export async function disconnect(id: string): Promise<void> {
  try {
    await backend.disconnect(id);
  } catch (err) {
    reportOperation(hostsStore.get().find((h) => h.id === id)?.name ?? id, err);
  }
}

export async function setHostInterval(id: string, ms: number): Promise<void> {
  const old = hostsStore.get().find((h) => h.id === id)?.interval_ms;
  patchHost(id, { interval_ms: ms });
  try {
    await backend.setInterval(id, ms);
  } catch (err) {
    if (old !== undefined) {
      patchHost(id, { interval_ms: old });
    }
    reportOperation(hostsStore.get().find((h) => h.id === id)?.name ?? id, err);
  }
}

export async function setHostRange(id: string, r: TimeRange): Promise<void> {
  const old = hostsStore.get().find((h) => h.id === id)?.range;
  patchHost(id, { range: r });
  try {
    await backend.setRange(id, r);
  } catch (err) {
    if (old) {
      patchHost(id, { range: old });
    }
    reportOperation(hostsStore.get().find((h) => h.id === id)?.name ?? id, err);
  }
}

// 以下為畫面的切換.

export function showPage(page: Page): void {
  uiStore.update((u) => ({ ...u, page }));
}

// selectHost 於主區域顯示監看畫面; 分頁顯示時切換至該主機, 同時顯示時捲動至其區塊.
export function selectHost(id: string): void {
  uiStore.update((u) => ({
    ...u,
    page: "monitor",
    selected: id,
    scrollTo: { id, seq: (u.scrollTo?.seq ?? 0) + 1 },
  }));
}

// showRecordings 為側欄 "記錄檔" 的行為: 已有開啟中的記錄檔時顯示歷史畫面, 否則顯示
// 記錄檔清單. 已在記錄檔清單時不變.
export function showRecordings(): void {
  uiStore.update((u) => {
    if (u.page === "recordings") {
      return u;
    }
    return { ...u, page: historyStore.get().paths.length > 0 ? "history" : "recordings" };
  });
}

// openRecordings 以 paths 取代歷史畫面開啟中的記錄檔並顯示歷史畫面.
export function openRecordings(paths: string[]): void {
  if (paths.length === 0) {
    return;
  }
  historyStore.set({ paths, left: null });
  showPage("history");
}

// forgetRecordings 於記錄檔被刪除後自歷史畫面開啟中的記錄檔移除.
export function forgetRecordings(paths: string[]): void {
  const h = historyStore.get();
  const rest = h.paths.filter((p) => !paths.includes(p));
  if (rest.length !== h.paths.length) {
    historyStore.set({ paths: rest, left: rest.length === 0 ? null : h.left });
  }
}

export function setHistoryLeft(left: number): void {
  historyStore.update((h) => ({ ...h, left }));
}

// reportOperationFailure 把某項操作失敗的原因顯示於錯誤視窗.
export function reportOperationFailure(name: string, error: ServiceError): void {
  operationErrorsStore.update((list) => [...list, { name, error }]);
}

export function setHostCollapsed(id: string, collapsed: boolean): void {
  uiStore.update((u) => ({ ...u, collapsedHosts: { ...u.collapsedHosts, [id]: collapsed } }));
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
