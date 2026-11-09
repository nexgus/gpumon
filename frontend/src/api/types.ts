// 前端使用的後端資料型別. 結構與 frontend/bindings/ 下自動產生的型別相同, 但以字串
// 聯集取代列舉, 使預覽頁的假資料與程式內的字面值可直接使用.

export type HostState = "disconnected" | "connecting" | "connected" | "reconnecting";

export type Appearance = "system" | "light" | "dark";
export type Language = "en" | "zh-TW";
export type TimeRange = "1m" | "5m" | "15m" | "30m" | "60m" | "3h" | "6h" | "12h" | "24h" | "all";
export type Curve = "util" | "mem" | "temp" | "fan" | "power" | "clock_gpu" | "clock_mem";
export type Field =
  | "util"
  | "mem_used"
  | "temp"
  | "power_w"
  | "clock_gpu"
  | "clock_mem"
  | "fan"
  | "enc"
  | "dec";

export type ErrorKind =
  | "connect"
  | "auth"
  | "host-key"
  | "platform"
  | "upload"
  | "exec"
  | "protocol-too-new"
  | "config"
  | "first-message"
  | "protocol-mismatch";

export type ErrorCode =
  | "invalid"
  | "not-found"
  | "duplicate"
  | "user-required"
  | "busy"
  | "active"
  | "failed"
  | "closed";

// HostConn 為主機表單的四個欄位. port 為 0 表示留空.
export interface HostConn {
  addr: string;
  port: number;
  user: string;
  key_file: string;
}

export interface HostStatus extends HostConn {
  id: string;
  name: string;
  interval_ms: number;
  state: HostState;
  reason: string;
  retries: number;
  max_retries: number;
  retry_error: string;
  recording: string;
}

export interface Device {
  id: number;
  vendor: string;
  name: string;
  mem_total?: number | null;
  uuid?: string;
  power_max_w?: number | null;
  clock_gpu_max?: number | null;
  clock_mem_max?: number | null;
  pci_vendor: string;
  pci_device: string;
  pci_subsys_vendor: string;
  pci_subsys_device: string;
}

export interface Devices {
  type: string;
  protocol: number;
  version: string;
  hostname: string;
  gpus: Device[];
}

export interface LiveGPU {
  id: number;
  util: number | null;
  mem_used: number | null;
  temp: number | null;
  power_w: number | null;
  clock_gpu: number | null;
  clock_mem: number | null;
  fan: number | null;
  enc: number | null;
  dec: number | null;
  util_computed: boolean;
}

export interface LiveProcess {
  gpu: number;
  pid: number;
  user: string;
  mem: number | null;
  util: number | null;
  cmd: string;
}

// Live 為一台主機的即時資料; time 為 Unix 微秒.
export interface Live {
  host: string;
  devices: Devices | null;
  has_sample: boolean;
  time: number;
  interval_ms: number;
  gpus: LiveGPU[] | null;
  procs: LiveProcess[] | null;
}

export interface DevicesEvent {
  host: string;
  devices: Devices;
}

export interface ConnectError {
  host: string;
  name: string;
  kind: ErrorKind;
  message: string;
}

export interface Prompt {
  id: string;
  host: string;
  name: string;
  kind: "password" | "passphrase";
  user: string;
  server: string;
  key_file: string;
  attempt: number;
}

export interface PromptClosed {
  id: string;
}

// UploadNotice 為上傳遠端程式之前的通知. update 為真表示更新, 為假表示首次上傳;
// remote_version 於首次上傳時為空字串. path 恆為 ~/.gpumon/gpumon.
export interface UploadNotice {
  id: string;
  host: string;
  name: string;
  path: string;
  update: boolean;
  remote_version: string;
  new_version: string;
}

export interface UploadClosed {
  id: string;
}

// SeriesValues 為一條曲線的數值, 留空的位置為 null.
export type SeriesValues = (number | null)[];

export interface SeriesData {
  avg: SeriesValues;
  min?: SeriesValues;
  max?: SeriesValues;
}

export interface GPUCurves {
  id: number;
  fields: Partial<Record<Field, SeriesData | null>>;
}

export type GapReason = "unknown" | "not_connected" | "error" | "disconnect";

export interface Gap {
  start: number;
  end: number;
  reason: GapReason;
  message?: string;
}

// QueryResult 為曲線查詢的結果, 意義見 internal/history 的 Result.
export interface QueryResult {
  from: number;
  to: number;
  width: number;
  binned: boolean;
  samples: number;
  times: number[];
  breaks: number[] | null;
  gpus: GPUCurves[] | null;
  gaps: Gap[] | null;
}

// RecordingFile 為記錄檔清單中的一個檔案. start 與 end 為檔案內第一筆與最後一筆
// 記錄的時刻 (Unix 微秒); active 表示該檔案為連線中, 不可刪除.
export interface RecordingFile {
  path: string;
  name: string;
  start: number;
  end: number;
  has_data: boolean;
  size: number;
  num_gpus: number;
  active: boolean;
}

export interface RecordingGroup {
  host: string;
  files: RecordingFile[];
  total_size: number;
}

// SkippedRecording 為無法開啟或格式不符的檔案, error 為英文原因.
export interface SkippedRecording {
  path: string;
  size: number;
  error: string;
}

export interface RecordingListing {
  hosts: RecordingGroup[];
  skipped: SkippedRecording[];
}

export interface DeleteResult {
  path: string;
  code: ErrorCode | "";
  error: string;
}

// RecordingInfo 為記錄檔說明區的內容與起訖時刻.
export interface RecordingInfo {
  path: string;
  host: string;
  devices: Devices;
  start: number;
  end: number;
  has_data: boolean;
  active: boolean;
}

// RecordedProcesses 為一筆程序記錄; time 為該筆的時刻 (Unix 微秒).
export interface RecordedProcesses {
  time: number;
  procs: LiveProcess[];
}

export interface WindowSettings {
  width: number;
  height: number;
  has_position: boolean;
  x: number;
  y: number;
}

export interface SettingsData {
  version: number;
  window: WindowSettings;
  appearance: Appearance;
  language: Language;
  monitor_range: TimeRange;
  history_range: TimeRange;
  curves: Curve[] | null;
  collapsed_gpus: Record<string, number[] | undefined> | null;
}

export interface Choices {
  monitor_ranges: TimeRange[];
  history_ranges: TimeRange[];
  curves: Curve[];
  max_curves: number;
  min_interval_ms: number;
  max_interval_ms: number;
}

// ServiceError 為服務方法失敗時的錯誤內容; message 為英文.
export interface ServiceError {
  code: ErrorCode;
  message: string;
}

// AboutInfo 為關於分頁所顯示的程式資訊. build_date 為 ISO 8601 格式或 unknown;
// webview2_version 只於 Windows 有值, WebView 本身的版本由前端自 user agent 取得.
export interface AboutInfo {
  name: string;
  version: string;
  build_date: string;
  go_version: string;
  os_name: string;
  os_version: string;
  platform: string;
  webview2_version: string;
}

// UISettings 為外觀與語言, 於設定變更後送給所有視窗.
export interface UISettings {
  appearance: Appearance;
  language: Language;
}

// QuitRequest 為結束確認視窗的內容, count 為將中斷連線的主機數.
export interface QuitRequest {
  count: number;
}

export interface ShowPage {
  page: "settings";
}

export type AboutTabName = "about" | "licenses";

export interface AboutTab {
  tab: AboutTabName;
}

export interface BackendEvents {
  "monitor:host": HostStatus;
  "monitor:sample": Live;
  "monitor:devices": DevicesEvent;
  "monitor:error": ConnectError;
  "monitor:prompt": Prompt;
  "monitor:prompt-closed": PromptClosed;
  "monitor:upload": UploadNotice;
  "monitor:upload-closed": UploadClosed;
  "app:ui-settings": UISettings;
  "app:quit-request": QuitRequest;
  "app:show-page": ShowPage;
  "app:about-tab": AboutTab;
}

// Backend 為前端呼叫後端的唯一介面. 正式版由 api/backend.ts 以 Wails bindings 實作,
// 預覽頁以假資料實作同一介面. 方法失敗時 promise 以 ServiceError 為 cause 的錯誤
// rejected, 以 api/errors.ts 的 serviceError 取出.
export interface Backend {
  hosts(): Promise<HostStatus[]>;
  addHost(conn: HostConn): Promise<HostStatus>;
  updateHost(id: string, conn: HostConn): Promise<HostStatus>;
  removeHost(id: string): Promise<void>;
  connect(id: string): Promise<void>;
  disconnect(id: string): Promise<void>;
  setInterval(id: string, ms: number): Promise<void>;
  answerPrompt(promptID: string, secret: string): Promise<void>;
  cancelPrompt(promptID: string): Promise<void>;
  confirmUpload(noticeID: string): Promise<void>;
  liveAll(): Promise<Live[]>;
  live(id: string): Promise<Live>;
  query(id: string, from: number, to: number, width: number, fields: Field[]): Promise<QueryResult | null>;
  chooseKeyFile(title: string): Promise<string>;

  recordings(): Promise<RecordingListing>;
  deleteRecordings(paths: string[]): Promise<DeleteResult[]>;
  recordingInfo(path: string): Promise<RecordingInfo>;
  queryRecording(path: string, from: number, to: number, width: number, fields: Field[]): Promise<QueryResult | null>;
  processesAt(path: string, t: number): Promise<RecordedProcesses | null>;

  getSettings(): Promise<SettingsData>;
  choices(): Promise<Choices>;
  setAppearance(v: Appearance): Promise<void>;
  setLanguage(v: Language): Promise<void>;
  setMonitorRange(v: TimeRange): Promise<void>;
  setHistoryRange(v: TimeRange): Promise<void>;
  setCurves(curves: Curve[]): Promise<void>;
  setGPUCollapsed(hostID: string, gpuID: number, collapsed: boolean): Promise<void>;

  about(): Promise<AboutInfo>;
  // confirmQuit 於使用者在結束確認視窗按下 "結束" 後呼叫, 程式隨即結束.
  confirmQuit(): Promise<void>;
  // copyText 把文字複製到剪貼簿; openURL 以系統預設瀏覽器開啟網址.
  copyText(text: string): Promise<void>;
  openURL(url: string): Promise<void>;

  // on 註冊事件的處理函數, 回傳取消註冊的函數.
  on<K extends keyof BackendEvents>(name: K, cb: (data: BackendEvents[K]) => void): () => void;
}
