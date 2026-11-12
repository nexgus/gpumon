// 前端使用的後端資料型別. 結構與 frontend/bindings/ 下自動產生的型別相同, 但以字串
// 聯集取代列舉, 使預覽頁的假資料與程式內的字面值可直接使用.

export type HostState = "disconnected" | "connecting" | "connected" | "reconnecting";

export type Appearance = "system" | "light" | "dark";
export type Language = "en" | "zh-TW";
export type SizeUnits = "binary" | "decimal";
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
  // util 為該程序的 GPU 使用率 (百分比整數); 歷史記錄取不到時沒有此成員.
  util?: number | null;
  cmd: string;
}

// Live 為一台主機的即時資料; time 為 Unix 微秒.
export interface Live {
  host: string;
  devices: Devices | null;
  // conn_start 為本次連線第一筆取樣的時刻; 本次連線尚未收到取樣時為 0.
  conn_start: number;
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

// sum 與 count 只於以 stats 查詢且降採樣時提供, 為各像素範圍內有值的取樣的總和與筆數.
export interface SeriesData {
  avg: SeriesValues;
  min?: SeriesValues;
  max?: SeriesValues;
  sum?: SeriesValues;
  count?: number[];
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
// last 為範圍內最後一筆取樣的時刻; counts 只於以 stats 查詢且降採樣時提供, 為各像素
// 範圍內的取樣筆數.
export interface QueryResult {
  from: number;
  to: number;
  width: number;
  binned: boolean;
  samples: number;
  last: number;
  counts?: number[] | null;
  times: number[];
  breaks: number[] | null;
  gpus: GPUCurves[] | null;
  gaps: Gap[] | null;
}

// FieldMax 為單張 GPU 單一欄位於整個記錄檔的最大值. runs 為最大值的出現處, 依時刻遞增,
// 每個元素為該處第一筆與最後一筆取樣的時刻 (Unix 微秒).
export interface FieldMax {
  value: number;
  runs: [number, number][];
}

// GPUMaxima 為單張 GPU 各欄位的最大值; 整個記錄檔沒有數值的欄位不含該鍵.
export interface GPUMaxima {
  id: number;
  fields: Partial<Record<Field, FieldMax>>;
}

export interface RecordingMaxima {
  gpus: GPUMaxima[];
}

// SampleGPU 為一筆取樣中單張 GPU 的數值, 無值的欄位為 null.
export interface SampleGPU {
  id: number;
  util: number | null;
  mem_used: number | null;
  temp: number | null;
  fan: number | null;
  power_w: number | null;
  clock_gpu: number | null;
  clock_mem: number | null;
}

// RecordedSample 為記錄檔中的一筆取樣; time 為該筆的時刻 (Unix 微秒).
export interface RecordedSample {
  time: number;
  gpus: SampleGPU[];
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

// ProcColumn 為程序卡片可排序的欄位, 依序為 GPU, PID, USER, GPU UTIL,
// GPU MEM 與 COMMAND.
export type ProcColumn = "gpu" | "pid" | "user" | "util" | "mem" | "command";
export type SortOrder = "asc" | "desc";

export interface ProcSort {
  column: ProcColumn;
  order: SortOrder;
}

// ProcFilter 為程序卡片的過濾條件, 各項為空時表示未設定. gpus 由小到大且不重複;
// user 與 command 為部分相符的文字, 不含前後空白.
export interface ProcFilter {
  gpus: number[] | null;
  user: string;
  command: string;
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
  // size_units 為記憶體與檔案大小的單位制, binary 為 1024 進位, decimal 為 1000 進位.
  size_units: SizeUnits;
  // monitor_span_ms 與 history_span_ms 為監看畫面與歷史畫面的顯示範圍長度 (毫秒);
  // history_all 為歷史畫面是否處於顯示全部狀態.
  monitor_span_ms: number;
  history_span_ms: number;
  history_all: boolean;
  curves: Curve[] | null;
  collapsed_gpus: Record<string, number[] | undefined> | null;
  // collapsed_procs 為程序卡片已收合的主機識別字串.
  collapsed_procs: string[] | null;
  // collapsed_maxima 為歷史畫面最大值卡片已收合的主機識別字串.
  collapsed_maxima: string[] | null;
  // proc_sorts 與 proc_filters 以主機識別字串為鍵, 為程序卡片的排序方式與過濾條件;
  // 預設排序方式與沒有任何條件的過濾條件不存放.
  proc_sorts: Record<string, ProcSort | undefined> | null;
  proc_filters: Record<string, ProcFilter | undefined> | null;
}

// min_span_ms 與 max_monitor_span_ms 為顯示範圍長度的下限與監看畫面的上限 (毫秒).
export interface Choices {
  curves: Curve[];
  max_curves: number;
  min_span_ms: number;
  max_monitor_span_ms: number;
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
  answerPrompt(promptID: string, secret: string): Promise<void>;
  cancelPrompt(promptID: string): Promise<void>;
  confirmUpload(noticeID: string): Promise<void>;
  liveAll(): Promise<Live[]>;
  live(id: string): Promise<Live>;
  // stats 為真時, 降採樣的結果另附 counts, sum 與 count, 見 QueryResult.
  query(id: string, from: number, to: number, width: number, fields: Field[], stats: boolean): Promise<QueryResult | null>;
  chooseKeyFile(title: string): Promise<string>;

  recordings(): Promise<RecordingListing>;
  deleteRecordings(paths: string[]): Promise<DeleteResult[]>;
  recordingInfo(path: string): Promise<RecordingInfo>;
  queryRecording(path: string, from: number, to: number, width: number, fields: Field[]): Promise<QueryResult | null>;
  processesAt(path: string, t: number): Promise<RecordedProcesses | null>;
  // maxima 回傳記錄檔中每張 GPU 每個欄位於整個記錄檔的最大值與其出現處.
  maxima(path: string): Promise<RecordingMaxima>;
  // sampleAt 回傳記錄檔中時刻不晚於 t 的最近一筆取樣; 沒有, 距 t 超過取樣間隔的 2 倍,
  // 或其與 t 之間有斷線記錄時為 null.
  sampleAt(path: string, t: number): Promise<RecordedSample | null>;

  getSettings(): Promise<SettingsData>;
  choices(): Promise<Choices>;
  setAppearance(v: Appearance): Promise<void>;
  setLanguage(v: Language): Promise<void>;
  setSizeUnits(v: SizeUnits): Promise<void>;
  setMonitorSpan(ms: number): Promise<void>;
  setHistorySpan(ms: number, all: boolean): Promise<void>;
  setCurves(curves: Curve[]): Promise<void>;
  setGPUCollapsed(hostID: string, gpuID: number, collapsed: boolean): Promise<void>;
  setProcessesCollapsed(hostID: string, collapsed: boolean): Promise<void>;
  setMaximaCollapsed(hostID: string, collapsed: boolean): Promise<void>;
  setProcessSort(hostID: string, sort: ProcSort): Promise<void>;
  setProcessFilter(hostID: string, filter: ProcFilter): Promise<void>;

  about(): Promise<AboutInfo>;
  // confirmQuit 於使用者在結束確認視窗按下 "結束" 後呼叫, 程式隨即結束.
  confirmQuit(): Promise<void>;
  // copyText 把文字複製到剪貼簿; openURL 以系統預設瀏覽器開啟網址.
  copyText(text: string): Promise<void>;
  openURL(url: string): Promise<void>;

  // on 註冊事件的處理函數, 回傳取消註冊的函數.
  on<K extends keyof BackendEvents>(name: K, cb: (data: BackendEvents[K]) => void): () => void;
}
