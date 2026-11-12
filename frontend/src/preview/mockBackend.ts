// 預覽頁用的假後端, 以 vite.preview.config.ts 取代 api/backend.ts, 不打包進正式產出.
// 主機與數值依設計稿的範例資料; 曲線為時刻的確定函數, 使截圖可重現.
import type {
  Backend,
  BackendEvents,
  Choices,
  Device,
  Devices,
  Field,
  GPUCurves,
  HostConn,
  HostStatus,
  Live,
  ProcColumn,
  RecordedProcesses,
  RecordedSample,
  RecordingMaxima,
  FieldMax,
  RecordingGroup,
  RecordingInfo,
  SkippedRecording,
  LiveGPU,
  QueryResult,
  SeriesData,
  SettingsData,
} from "../api/types";

const GiB = 1024 ** 3;
const MiB = 1024 ** 2;
const params = new URLSearchParams(location.search);
// now 為假資料的 "目前時刻"; 傳入 now=<Unix 秒> 時固定, 使截圖的時間軸可重現.
const fixedNow = params.get("now") ? Number(params.get("now")) * 1e6 : 0;
const startWall = Date.now() * 1000;
function now(): number {
  return fixedNow ? fixedNow + (Date.now() * 1000 - startWall) : Date.now() * 1000;
}
const live = params.get("live") !== "0";

type Listener = (data: unknown) => void;
const listeners = new Map<string, Set<Listener>>();

function emit<K extends keyof BackendEvents>(name: K, data: BackendEvents[K]): void {
  for (const l of listeners.get(name) ?? []) {
    l(data);
  }
}

function fail(code: string, message: string): Error {
  return Object.assign(new Error(message), { cause: { code, message } });
}

function nvidia(id: number, name: string, mem: number, power: number, cg: number, cm: number): Device {
  return {
    id,
    vendor: "nvidia",
    name,
    mem_total: mem,
    power_max_w: power,
    clock_gpu_max: cg,
    clock_mem_max: cm,
    pci_vendor: "10de",
    pci_device: "2bb1",
    pci_subsys_vendor: "10de",
    pci_subsys_device: "204b",
  };
}

const intel: Device = {
  id: 2,
  vendor: "intel",
  name: "Intel UHD Graphics 770",
  clock_gpu_max: 1550,
  pci_vendor: "8086",
  pci_device: "a780",
  pci_subsys_vendor: "1043",
  pci_subsys_device: "8882",
};

function devices(hostname: string, gpus: Device[]): Devices {
  return { type: "devices", protocol: 1, version: "0.1.0", hostname, gpus };
}

// GUI 一律以 200 ms 取樣.
const intervalMS = 200;

interface MockHost {
  status: HostStatus;
  devices: Devices | null;
  // lastAt 為重新連線中的主機最後一筆取樣距今的微秒數.
  lastAgo: number;
  seed: number;
}

function status(id: string, addr: string, user: string, state: HostStatus["state"], extra: Partial<HostStatus> = {}): HostStatus {
  return {
    id,
    name: id,
    addr,
    port: 0,
    user,
    key_file: "",
    state,
    reason: "",
    retries: 0,
    max_retries: 5,
    retry_error: "",
    recording: "",
    ...extra,
  };
}

const scenario = params.get("scenario") ?? "design";

function initialHosts(): MockHost[] {
  if (scenario === "empty") {
    return [];
  }
  const list: MockHost[] = [
    {
      status: status("sweetener@10.90.20.55", "10.90.20.55", "sweetener", "connected"),
      devices: devices("ai-x-dev", [
        nvidia(0, "NVIDIA RTX PRO 6000 Blackwell", 95.6 * GiB, 600, 2850, 10501),
        nvidia(1, "NVIDIA RTX PRO 6000 Blackwell", 95.6 * GiB, 600, 2850, 10501),
        intel,
      ]),
      lastAgo: 0,
      seed: 1,
    },
    {
      status: status("alice@gpu-lab-02", "gpu-lab-02", "alice", "reconnecting", {
        reason: "connection timed out",
        retries: 2,
      }),
      devices: devices("gpu-lab-02", [nvidia(0, "NVIDIA GeForce RTX 4090", 24 * GiB, 450, 2850, 10501)]),
      lastAgo: 31e6,
      seed: 2,
    },
    {
      status: status("bob@render-farm", "render-farm", "bob", "disconnected", { reason: "no response from the remote host" }),
      devices: null,
      lastAgo: 0,
      seed: 3,
    },
    {
      status: status("carol@10.0.8.21", "10.0.8.21", "carol", "connecting"),
      devices: null,
      lastAgo: 0,
      seed: 4,
    },
  ];
  if (scenario === "nogpu") {
    list.push({
      status: status("erin@build-01", "build-01", "erin", "connected"),
      devices: devices("build-01", []),
      lastAgo: 0,
      seed: 5,
    });
  }
  return list;
}

const hosts = initialHosts();

const settings: SettingsData = {
  version: 1,
  window: { width: 1280, height: 800, has_position: false, x: 0, y: 0 },
  appearance: (params.get("theme") as SettingsData["appearance"]) ?? "system",
  language: (params.get("lang") as SettingsData["language"]) ?? "en",
  size_units: params.get("units") === "decimal" ? "decimal" : "binary",
  monitor_span_ms: Number(params.get("span") ?? 300_000),
  history_span_ms: Number(params.get("hspan") ?? 300_000),
  history_all: params.get("hall") === "1",
  curves: (params.get("curves")?.split(",") as SettingsData["curves"]) ?? ["util", "mem"],
  collapsed_gpus: params.get("collapse") === "1" ? { "sweetener@10.90.20.55": [2] } : {},
  collapsed_procs: params.get("pcollapse") === "1" ? ["sweetener@10.90.20.55"] : [],
  collapsed_maxima: params.get("mcollapse") === "1" ? ["sweetener@10.90.20.55"] : [],
  // psort 為 <欄位>-<方向>, 例如 mem-desc; pfilter=1 設定 USER 與 COMMAND 兩項條件,
  // pfilter=none 設定沒有任何程序符合的條件. 皆套用於主要主機.
  proc_sorts: params.get("psort")
    ? {
        "sweetener@10.90.20.55": {
          column: params.get("psort")!.split("-")[0] as ProcColumn,
          order: params.get("psort")!.split("-")[1] === "desc" ? "desc" : "asc",
        },
      }
    : {},
  proc_filters:
    params.get("pfilter") === "1"
      ? { "sweetener@10.90.20.55": { gpus: [], user: "mlin", command: "python" } }
      : params.get("pfilter") === "none"
        ? { "sweetener@10.90.20.55": { gpus: [], user: "", command: "blender" } }
        : {},
};

const choices: Choices = {
  curves: ["util", "mem", "temp", "fan", "power", "clock_gpu", "clock_mem"],
  max_curves: 7,
  min_span_ms: 60_000,
  max_monitor_span_ms: 86_400_000,
};

// wave 為平滑的確定函數, 值域約為 0 至 1.
function wave(t: number, seed: number, period: number): number {
  const s = t / 1e6;
  return (
    0.5 +
    0.3 * Math.sin(s / period + seed) +
    0.15 * Math.sin(s / (period / 3.7) + seed * 2) +
    0.05 * Math.sin(s * 1.3 + seed * 5)
  );
}

function value(h: MockHost, gpu: Device, f: Field, t: number): number | null {
  const k = h.seed * 10 + gpu.id;
  if (gpu.vendor === "intel") {
    if (f === "util") {
      return Math.round(3 + 4 * wave(t, k, 30));
    }
    if (f === "clock_gpu") {
      return Math.round(300 + 200 * wave(t, k, 50));
    }
    return null;
  }
  const busy = gpu.id === 0 ? 0.85 : 0.35;
  const w = wave(t, k, 25);
  switch (f) {
    case "util":
      return Math.max(0, Math.min(100, Math.round(busy * 100 * (0.75 + 0.4 * w))));
    case "mem_used":
      return Math.floor((gpu.mem_total ?? 0) * (busy * 0.65 + 0.05 * wave(t, k + 1, 90)));
    case "temp":
      return Math.round(50 + 30 * busy * (0.6 + 0.5 * wave(t, k + 2, 60)));
    case "fan":
      return Math.round(25 + 45 * busy * (0.6 + 0.6 * wave(t, k + 3, 70)) + (gpu.id === 0 && h.seed === 1 ? 60 * wave(t, 9, 40) : 0));
    case "power_w":
      return Math.round((gpu.power_max_w ?? 300) * busy * (0.55 + 0.4 * w));
    case "clock_gpu":
      return Math.round(1500 + 1250 * busy * (0.7 + 0.3 * w));
    case "clock_mem":
      return gpu.id === 0 && h.seed === 1 ? Math.round(10600 + 400 * wave(t, k + 4, 50)) : 10501;
  }
  return null;
}

const step = intervalMS * 1000;

function lastTime(h: MockHost): number {
  return Math.floor((now() - h.lastAgo) / step) * step;
}

// mainGap 為主要主機的假無資料時段, 起訖為取樣時刻; 開啟預覽頁時約位於 5 分鐘顯示範圍的
// 28% 至 36% 處, 之後隨時間左移.
const mainGap: QueryGap = {
  start: Math.floor((now() - 0.72 * 300e6) / step) * step,
  end: Math.floor((now() - 0.64 * 300e6) / step) * step,
  reason: "error",
  message: "nvml timeout",
};

function gapsOf(h: MockHost, from: number, to: number): QueryGap[] {
  return h.seed === 1 && mainGap.end >= from && mainGap.start <= to ? [mainGap] : [];
}

function sampleTimes(h: MockHost, from: number, to: number): number[] {
  const last = Math.min(to, lastTime(h));
  const gaps = gapsOf(h, from, to);
  const out: number[] = [];
  for (let t = Math.ceil(from / step) * step; t <= last; t += step) {
    if (gaps.some((g) => t > g.start && t < g.end)) {
      continue;
    }
    out.push(t);
  }
  return out;
}

// mockConnStart 為預覽頁假設的連線起點; 預覽頁的曲線為合成資料, 不隨連線起點截斷.
const mockConnStart = Math.floor(now() / step) * step;

function makeLive(h: MockHost): Live {
  const t = lastTime(h);
  const gpus: LiveGPU[] = (h.devices?.gpus ?? []).map((g) => ({
    id: g.id,
    util: value(h, g, "util", t),
    mem_used: value(h, g, "mem_used", t),
    temp: value(h, g, "temp", t),
    power_w: value(h, g, "power_w", t),
    clock_gpu: value(h, g, "clock_gpu", t),
    clock_mem: value(h, g, "clock_mem", t),
    fan: value(h, g, "fan", t),
    enc: 0,
    dec: 0,
    util_computed: g.vendor === "intel",
  }));
  const procs =
    h.seed === 1
      ? [
          { gpu: 0, pid: 48211, user: "sweetener", mem: 41.8 * GiB, util: 80, cmd: "python train.py --config configs/vit_l.yaml" },
          { gpu: 0, pid: 48377, user: "mlin", mem: 16.6 * GiB, util: 7, cmd: "python -m vllm.entrypoints.openai.api_server" },
          { gpu: 1, pid: 51902, user: "sweetener", mem: 22.1 * GiB, util: 34, cmd: "python eval.py --ckpt runs/1104/last.pt --data /mnt/datasets/imagenet-21k/train --workers 32 --batch-size 256 --precision bf16 --log-dir /home/sweetener/experiments/2026-11/vit_l_eval_run_0042" },
          { gpu: 2, pid: 2217, user: "sweetener", mem: null, util: null, cmd: "/usr/lib/xorg/Xorg" },
          { gpu: 1, pid: 50871, user: "mlin", mem: 21.3 * GiB, util: 41, cmd: "python -u /home/mlin/experiments/finetune/run_finetune.py --config configs/lora_r64.yaml --output-dir /data/mlin/runs/1104 --resume latest" },
          { gpu: 0, pid: 49102, user: "Kchen", mem: 3.2 * GiB, util: 2, cmd: "jupyter-lab --no-browser --port 8890" },
          { gpu: 1, pid: 52310, user: "kchen", mem: 812 * MiB, util: 0, cmd: "Python bench/matmul.py --size 8192" },
          { gpu: 2, pid: 3150, user: "gdm", mem: null, util: null, cmd: "/usr/bin/gnome-shell" },
        ]
      : h.seed === 2
        ? [{ gpu: 0, pid: 9921, user: "alice", mem: 21.2 * GiB, util: 95, cmd: "python finetune.py --epochs 40" }]
        : [];
  const showsData = h.status.state === "connected" || h.status.state === "reconnecting";
  return {
    host: h.status.id,
    devices: h.devices,
    conn_start: showsData ? mockConnStart : 0,
    has_sample: showsData && !!h.devices,
    time: t,
    interval_ms: intervalMS,
    gpus,
    procs,
  };
}

interface QueryGap {
  start: number;
  end: number;
  reason: "unknown" | "not_connected" | "error" | "disconnect";
  message?: string;
}

// query 依 internal/history 的規則產生查詢結果: 取樣筆數不多於 width 時回傳原始取樣點,
// 否則把 [from, to] 均分為 width 個像素範圍取最大, 最小與平均; stats 為真時另附統計.
function query(
  h: MockHost,
  from: number,
  to: number,
  width: number,
  fields: Field[],
  stats = false,
  times: number[] = sampleTimes(h, from, to),
  gaps: QueryGap[] = gapsOf(h, from, to),
): QueryResult {
  const gpus = h.devices?.gpus ?? [];
  const binned = times.length > width;
  const between = (a: number, b: number) => gaps.some((g) => a <= g.start && b >= g.end);
  const last = times.length > 0 ? times[times.length - 1] : 0;
  if (!binned) {
    const breaks: number[] = [];
    for (let i = 0; i + 1 < times.length; i++) {
      if (between(times[i], times[i + 1])) {
        breaks.push(i);
      }
    }
    const curves: GPUCurves[] = gpus.map((g) => {
      const f: Partial<Record<Field, SeriesData>> = {};
      for (const fld of fields) {
        f[fld] = { avg: times.map((t) => value(h, g, fld, t)) };
      }
      return { id: g.id, fields: f };
    });
    return { from, to, width, binned, samples: times.length, last, times, breaks, gpus: curves, gaps };
  }
  const span = to - from + 1;
  const binOf = (t: number) => Math.floor(((t - from) * width) / span);
  const binTimes = Array.from({ length: width }, (_, i) => from + Math.ceil((i * span) / width));
  const counts = new Array<number>(width).fill(0);
  const breaks: number[] = [];
  let prev = -1;
  for (const t of times) {
    const b = binOf(t);
    counts[b]++;
    if (prev >= 0 && b === binOf(prev) + 1 && between(prev, t)) {
      breaks.push(b - 1);
    }
    prev = t;
  }
  const curves: GPUCurves[] = gpus.map((g) => {
    const f: Partial<Record<Field, SeriesData>> = {};
    for (const fld of fields) {
      const sum = new Array<number>(width).fill(0);
      const count = new Array<number>(width).fill(0);
      const min: (number | null)[] = new Array(width).fill(null);
      const max: (number | null)[] = new Array(width).fill(null);
      for (const t of times) {
        const v = value(h, g, fld, t);
        if (v === null) {
          continue;
        }
        const b = binOf(t);
        min[b] = count[b] === 0 ? v : Math.min(min[b] as number, v);
        max[b] = count[b] === 0 ? v : Math.max(max[b] as number, v);
        sum[b] += v;
        count[b]++;
      }
      const avg = sum.map((x, i) => (count[i] === 0 ? null : x / count[i]));
      f[fld] = stats ? { avg, min, max, sum, count } : { avg, min, max };
    }
    return { id: g.id, fields: f };
  });
  return {
    from,
    to,
    width,
    binned,
    samples: times.length,
    last,
    counts: stats ? counts : undefined,
    times: binTimes,
    breaks,
    gpus: curves,
    gaps,
  };
}

function find(id: string): MockHost {
  const h = hosts.find((x) => x.status.id === id);
  if (!h) {
    throw fail("not-found", `Host ${id} not found`);
  }
  return h;
}

function idOf(c: HostConn): string {
  return c.user ? `${c.user}@${c.addr}` : c.addr;
}


// 記錄檔的假資料. 時刻為本機時區的固定日期, 與設計稿的範例一致.
interface MockRecording {
  path: string;
  host: string;
  start: number;
  end: number;
  size: number;
  active: boolean;
  gaps: QueryGap[];
}

function at(y: number, mo: number, d: number, h: number, mi: number, sec: number): number {
  return new Date(y, mo - 1, d, h, mi, sec).getTime() * 1000;
}

const MB = 1024 * 1024;

function rec(host: string, file: string, start: number, end: number, sizeMB: number, active = false, gaps: QueryGap[] = []): MockRecording {
  return { path: `/data/history/${host}/${file}.gpurec`, host, start, end, size: Math.round(sizeMB * MB), active, gaps };
}

const sw = "sweetener@10.90.20.55";
const al = "alice@gpu-lab-02";
const bo = "bob@render-farm";
const recordings: MockRecording[] = scenario === "empty" ? [] : [
  rec(sw, "20261105-091240", at(2026, 11, 5, 9, 12, 40), at(2026, 11, 5, 14, 32, 40), 1.0, true),
  rec(sw, "20261104-200211", at(2026, 11, 4, 20, 2, 11), at(2026, 11, 4, 23, 47, 30), 0.7, false, [
    { start: at(2026, 11, 4, 21, 15, 0), end: at(2026, 11, 4, 21, 19, 12), reason: "disconnect", message: "connection timed out" },
  ]),
  rec(sw, "20261103-083002", at(2026, 11, 3, 8, 30, 2), at(2026, 11, 4, 18, 11, 54), 6.2),
  rec(sw, "20261028-100517", at(2026, 10, 28, 10, 5, 17), at(2026, 11, 2, 22, 40, 9), 241.9),
  rec(sw, "20261027-164451", at(2026, 10, 27, 16, 44, 51), at(2026, 10, 27, 19, 2, 33), 18.5),
  rec(al, "20261105-135803", at(2026, 11, 5, 13, 58, 3), at(2026, 11, 5, 14, 32, 3), 0.2, true),
  rec(al, "20261104-210540", at(2026, 11, 4, 21, 5, 40), at(2026, 11, 4, 22, 10, 2), 0.3),
  rec(al, "20261030-090012", at(2026, 10, 30, 9, 0, 12), at(2026, 11, 3, 17, 26, 45), 131.1),
  rec(bo, "20261101-112035", at(2026, 11, 1, 11, 20, 35), at(2026, 11, 2, 7, 48, 10), 12.8),
];
const skippedRecordings: SkippedRecording[] = scenario === "empty" ? [] : [
  { path: `/data/history/${sw}/20260920-101010.gpurec`, size: 2048, error: "unsupported format version 3" },
  { path: `/data/history/${bo}/20260921-090000.gpurec`, size: 0, error: "file is truncated before the end of the header" },
];

function hostOfRecording(r: MockRecording): MockHost {
  const h = hosts.find((x) => x.status.id === r.host) ?? hosts[0];
  return h;
}

function recGroups(): RecordingGroup[] {
  const out: RecordingGroup[] = [];
  for (const host of [sw, al, bo]) {
    const files = recordings
      .filter((r) => r.host === host)
      .sort((a, b) => a.start - b.start)
      .map((r) => ({
        path: r.path,
        name: r.path.split("/").pop() ?? r.path,
        start: r.start,
        end: r.end,
        has_data: true,
        size: r.size,
        num_gpus: hostOfRecording(r).devices?.gpus.length ?? 0,
        active: r.active,
      }));
    if (files.length > 0) {
      out.push({ host, files, total_size: files.reduce((n, f) => n + f.size, 0) });
    }
  }
  return out;
}

function findRecording(path: string): MockRecording {
  const r = recordings.find((x) => x.path === path);
  if (!r) {
    throw fail("not-found", `${path} not found`);
  }
  return r;
}

function recordingInfo(r: MockRecording): RecordingInfo {
  const h = hostOfRecording(r);
  return {
    path: r.path,
    host: r.host,
    devices: h.devices ?? devices(r.host, []),
    start: r.start,
    end: r.end,
    has_data: true,
    active: r.active,
  };
}

// recordingStep 為假記錄檔的取樣間隔 (微秒), 使每個記錄檔約有 30000 筆取樣.
function recordingStep(r: MockRecording): number {
  return Math.max(1e6, Math.ceil((r.end - r.start) / 30000 / 1e6) * 1e6);
}

function recordingTimes(r: MockRecording, from: number, to: number): number[] {
  const step = recordingStep(r);
  const times: number[] = [];
  for (let t = Math.ceil(Math.max(from, r.start) / step) * step; t <= Math.min(to, r.end); t += step) {
    if (!r.gaps.some((g) => t > g.start && t < g.end)) {
      times.push(t);
    }
  }
  return times;
}

function recordingQuery(r: MockRecording, from: number, to: number, width: number, fields: Field[]): QueryResult {
  return query(hostOfRecording(r), from, to, width, fields, false, recordingTimes(r, from, to), r.gaps);
}

const maximaFields: Field[] = ["util", "mem_used", "temp", "fan", "power_w", "clock_gpu", "clock_mem"];

// recordingMaxima 依 internal/history 的規則求整個記錄檔的最大值: 出現處為數值等於最大值
// 的連續取樣, 中間隔著無資料時段即分為兩處.
function recordingMaxima(r: MockRecording): RecordingMaxima {
  const h = hostOfRecording(r);
  const times = recordingTimes(r, r.start, r.end);
  const gpus = (h.devices?.gpus ?? []).map((g) => {
    const fields: Partial<Record<Field, FieldMax>> = {};
    for (const f of maximaFields) {
      let m: FieldMax | null = null;
      let prev = -1;
      for (const t of times) {
        const v = value(h, g, f, t);
        const broken = prev >= 0 && r.gaps.some((x) => prev <= x.start && t >= x.end);
        const last = m?.runs[m.runs.length - 1];
        if (v === null) {
          prev = -1;
          continue;
        }
        if (!m || v > m.value) {
          m = { value: v, runs: [[t, t]] };
        } else if (v === m.value) {
          if (last && last[1] === prev && !broken) {
            last[1] = t;
          } else {
            m.runs.push([t, t]);
          }
        }
        prev = t;
      }
      if (m) {
        fields[f] = m;
      }
    }
    return { id: g.id, fields };
  });
  return { gpus };
}

// recordingSample 回傳時刻不晚於 t 的最近一筆取樣, 規則與 internal/history 的 SampleAt 相同.
function recordingSample(r: MockRecording, t: number): RecordedSample | null {
  const step = recordingStep(r);
  const at = Math.min(r.end, Math.floor(t / step) * step);
  if (at < r.start || t - at > 2 * step || r.gaps.some((g) => at < g.end && t > g.start)) {
    return null;
  }
  const h = hostOfRecording(r);
  return {
    time: at,
    gpus: (h.devices?.gpus ?? []).map((g) => ({
      id: g.id,
      util: value(h, g, "util", at),
      mem_used: value(h, g, "mem_used", at),
      temp: value(h, g, "temp", at),
      fan: value(h, g, "fan", at),
      power_w: value(h, g, "power_w", at),
      clock_gpu: value(h, g, "clock_gpu", at),
      clock_mem: value(h, g, "clock_mem", at),
    })),
  };
}

function recordingProcesses(r: MockRecording, t: number): RecordedProcesses | null {
  if (t < r.start) {
    return null;
  }
  const lv = makeLive(hostOfRecording(r));
  return { time: Math.floor(Math.min(t, r.end) / 4e6) * 4e6, procs: lv.procs ?? [] };
}

if (live) {
  window.setInterval(() => {
    for (const h of hosts) {
      if (h.status.state === "connected" && h.devices) {
        emit("monitor:sample", makeLive(h));
      }
    }
  }, intervalMS);
}

// mock 供預覽頁觸發事件, 例如輸入要求與錯誤視窗.
export const mock = {
  emit,
  hosts,
};

export const backend: Backend = {
  async hosts() {
    return hosts.map((h) => ({ ...h.status }));
  },
  async addHost(conn) {
    if (!conn.user) {
      throw fail("user-required", `No user name is specified for ${conn.addr} in the form or in the SSH configuration`);
    }
    const id = idOf(conn);
    if (hosts.some((h) => h.status.id === id)) {
      throw fail("duplicate", `${id} already exists`);
    }
    const h: MockHost = { status: status(id, conn.addr, conn.user, "disconnected"), devices: null, lastAgo: 0, seed: 6 };
    Object.assign(h.status, conn);
    hosts.push(h);
    return { ...h.status };
  },
  async updateHost(id, conn) {
    const h = find(id);
    if (h.status.state !== "disconnected") {
      throw fail("busy", `Disconnect ${id} before editing it`);
    }
    if (!conn.user) {
      throw fail("user-required", `No user name is specified for ${conn.addr} in the form or in the SSH configuration`);
    }
    Object.assign(h.status, conn, { id: idOf(conn), name: idOf(conn) });
    return { ...h.status };
  },
  async removeHost(id) {
    const i = hosts.findIndex((h) => h.status.id === id);
    if (i >= 0) {
      hosts.splice(i, 1);
    }
  },
  async connect(id) {
    const h = find(id);
    h.status = { ...h.status, state: "connecting", reason: "" };
    emit("monitor:host", { ...h.status });
  },
  async disconnect(id) {
    const h = find(id);
    h.status = { ...h.status, state: "disconnected", reason: "", retries: 0 };
    emit("monitor:host", { ...h.status });
  },
  async answerPrompt() {},
  async cancelPrompt() {},
  async confirmUpload() {},
  async liveAll() {
    return hosts.map(makeLive);
  },
  async live(id) {
    return makeLive(find(id));
  },
  async query(id, from, to, width, fields, stats) {
    return query(find(id), from, to, width, fields, stats);
  },
  async chooseKeyFile() {
    return "~/.ssh/lab_ed25519";
  },
  async recordings() {
    return { hosts: recGroups(), skipped: skippedRecordings.map((x) => ({ ...x })) };
  },
  async deleteRecordings(paths) {
    return paths.map((path) => {
      const i = recordings.findIndex((r) => r.path === path);
      if (i >= 0 && recordings[i].active) {
        return { path, code: "active" as const, error: "the recording is being written" };
      }
      if (i >= 0) {
        recordings.splice(i, 1);
        return { path, code: "" as const, error: "" };
      }
      const k = skippedRecordings.findIndex((x) => x.path === path);
      if (k >= 0) {
        skippedRecordings.splice(k, 1);
        return { path, code: "" as const, error: "" };
      }
      return { path, code: "not-found" as const, error: "no such file" };
    });
  },
  async recordingInfo(path) {
    return recordingInfo(findRecording(path));
  },
  async queryRecording(path, from, to, width, fields) {
    return recordingQuery(findRecording(path), from, to, width, fields);
  },
  async processesAt(path, t) {
    return recordingProcesses(findRecording(path), t);
  },
  async maxima(path) {
    return recordingMaxima(findRecording(path));
  },
  async sampleAt(path, t) {
    return recordingSample(findRecording(path), t);
  },
  async getSettings() {
    return structuredClone(settings);
  },
  async choices() {
    return choices;
  },
  async setAppearance() {},
  async setLanguage() {},
  async setSizeUnits(v) {
    settings.size_units = v;
  },
  async setMonitorSpan(ms) {
    settings.monitor_span_ms = ms;
  },
  async setHistorySpan(ms, all) {
    settings.history_span_ms = ms;
    settings.history_all = all;
  },
  async setCurves() {},
  async setGPUCollapsed() {},
  async setProcessesCollapsed(hostID, collapsed) {
    const rest = (settings.collapsed_procs ?? []).filter((id) => id !== hostID);
    settings.collapsed_procs = collapsed ? [...rest, hostID].sort() : rest;
  },
  async setMaximaCollapsed(hostID, collapsed) {
    const rest = (settings.collapsed_maxima ?? []).filter((id) => id !== hostID);
    settings.collapsed_maxima = collapsed ? [...rest, hostID].sort() : rest;
  },
  async setProcessSort(hostID, sort) {
    const all = { ...(settings.proc_sorts ?? {}) };
    if (sort.column === "gpu" && sort.order === "asc") {
      delete all[hostID];
    } else {
      all[hostID] = sort;
    }
    settings.proc_sorts = all;
  },
  async setProcessFilter(hostID, filter) {
    const all = { ...(settings.proc_filters ?? {}) };
    if ((filter.gpus?.length ?? 0) === 0 && filter.user === "" && filter.command === "") {
      delete all[hostID];
    } else {
      all[hostID] = filter;
    }
    settings.proc_filters = all;
  },
  async about() {
    const windows = params.get("platform") === "windows";
    return {
      name: "GPU Monitor",
      version: "0.1.0+3f2a9c1",
      build_date: "2026-11-05T10:42:17+08:00",
      go_version: "go1.26.0",
      os_name: windows ? "Windows 11 Pro" : "macOS",
      os_version: windows ? "2009 (Build: 26100)" : "15.6",
      platform: windows ? "windows/amd64" : "darwin/arm64",
      webview2_version: windows ? "142.0.3595.53" : "",
    };
  },
  async confirmQuit() {},
  async copyText(text) {
    console.log(`copy:\n${text}`);
  },
  async openURL(url) {
    console.log(`open: ${url}`);
  },
  on(name, cb) {
    let set = listeners.get(name);
    if (!set) {
      set = new Set();
      listeners.set(name, set);
    }
    const l = cb as Listener;
    set.add(l);
    return () => set.delete(l);
  },
};
