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
  RecordedProcesses,
  RecordingGroup,
  RecordingInfo,
  SkippedRecording,
  LiveGPU,
  QueryResult,
  SeriesData,
  SettingsData,
} from "../api/types";

const GiB = 1024 ** 3;
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

interface MockHost {
  status: HostStatus;
  devices: Devices | null;
  interval: number;
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
      interval: 1000,
      lastAgo: 0,
      seed: 1,
    },
    {
      status: status("alice@gpu-lab-02", "gpu-lab-02", "alice", "reconnecting", {
        reason: "connection timed out",
        retries: 2,
      }),
      devices: devices("gpu-lab-02", [nvidia(0, "NVIDIA GeForce RTX 4090", 24 * GiB, 450, 2850, 10501)]),
      interval: 500,
      lastAgo: 31e6,
      seed: 2,
    },
    {
      status: status("bob@render-farm", "render-farm", "bob", "disconnected", { reason: "no response from the remote host" }),
      devices: null,
      interval: 1000,
      lastAgo: 0,
      seed: 3,
    },
    {
      status: status("carol@10.0.8.21", "10.0.8.21", "carol", "connecting"),
      devices: null,
      interval: 1000,
      lastAgo: 0,
      seed: 4,
    },
  ];
  if (scenario === "nogpu") {
    list.push({
      status: status("erin@build-01", "build-01", "erin", "connected"),
      devices: devices("build-01", []),
      interval: 1000,
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
  monitor_range: (params.get("range") as SettingsData["monitor_range"]) ?? "5m",
  history_range: (params.get("hrange") as SettingsData["history_range"]) ?? "5m",
  curves: (params.get("curves")?.split(",") as SettingsData["curves"]) ?? ["util", "mem"],
  collapsed_gpus: params.get("collapse") === "1" ? { "sweetener@10.90.20.55": [2] } : {},
  interval_ms: 1000,
  collapsed_procs: params.get("pcollapse") === "1" ? ["sweetener@10.90.20.55"] : [],
};

const choices: Choices = {
  monitor_ranges: ["1m", "5m", "15m", "30m", "60m", "3h", "6h", "12h", "24h"],
  history_ranges: ["1m", "5m", "15m", "30m", "60m", "3h", "6h", "12h", "24h", "all"],
  curves: ["util", "mem", "temp", "fan", "power", "clock_gpu", "clock_mem"],
  max_curves: 7,
  min_interval_ms: 200,
  max_interval_ms: 10000,
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

function lastTime(h: MockHost): number {
  const step = h.interval * 1000;
  return Math.floor((now() - h.lastAgo) / step) * step;
}

// gapOf 回傳主機的假無資料時段: 主要主機約在顯示範圍的 30% 至 36% 處有一段.
function gapsOf(h: MockHost, from: number, to: number) {
  if (h.seed !== 1) {
    return [];
  }
  const span = to - from;
  const start = from + span * 0.28;
  const end = from + span * 0.36;
  return [{ start, end, reason: "error" as const, message: "nvml timeout" }];
}

function sampleTimes(h: MockHost, from: number, to: number): number[] {
  const step = h.interval * 1000;
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
          { gpu: 1, pid: 51902, user: "sweetener", mem: 22.1 * GiB, util: 34, cmd: "python eval.py --ckpt runs/1104/last.pt" },
          { gpu: 2, pid: 2217, user: "sweetener", mem: null, util: null, cmd: "/usr/lib/xorg/Xorg" },
        ]
      : h.seed === 2
        ? [{ gpu: 0, pid: 9921, user: "alice", mem: 21.2 * GiB, util: 95, cmd: "python finetune.py --epochs 40" }]
        : [];
  const showsData = h.status.state === "connected" || h.status.state === "reconnecting";
  return {
    host: h.status.id,
    devices: h.devices,
    has_sample: showsData && !!h.devices,
    time: t,
    interval_ms: h.interval,
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

function query(
  h: MockHost,
  from: number,
  to: number,
  width: number,
  fields: Field[],
  times: number[] = sampleTimes(h, from, to),
  gaps: QueryGap[] = gapsOf(h, from, to),
): QueryResult {
  const gpus = h.devices?.gpus ?? [];
  const binned = times.length > width;
  const breaks: number[] = [];
  if (!binned) {
    for (let i = 0; i + 1 < times.length; i++) {
      if (gaps.some((g) => times[i] <= g.start && times[i + 1] >= g.end)) {
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
    return { from, to, width, binned, samples: times.length, times, breaks, gpus: curves, gaps };
  }
  const span = (to - from) / width;
  const binTimes = Array.from({ length: width }, (_, i) => from + i * span);
  const bins: number[][] = binTimes.map(() => []);
  for (const t of times) {
    const i = Math.min(width - 1, Math.floor((t - from) / span));
    bins[i].push(t);
  }
  const curves: GPUCurves[] = gpus.map((g) => {
    const f: Partial<Record<Field, SeriesData>> = {};
    for (const fld of fields) {
      const avg: (number | null)[] = [];
      const min: (number | null)[] = [];
      const max: (number | null)[] = [];
      for (const b of bins) {
        const vs = b.map((t) => value(h, g, fld, t)).filter((v): v is number => v !== null);
        if (vs.length === 0) {
          avg.push(null);
          min.push(null);
          max.push(null);
        } else {
          avg.push(vs.reduce((a, c) => a + c, 0) / vs.length);
          min.push(Math.min(...vs));
          max.push(Math.max(...vs));
        }
      }
      f[fld] = { avg, min, max };
    }
    return { id: g.id, fields: f };
  });
  return { from, to, width, binned, samples: times.length, times: binTimes, breaks, gpus: curves, gaps };
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

function recordingQuery(r: MockRecording, from: number, to: number, width: number, fields: Field[]): QueryResult {
  const h = hostOfRecording(r);
  const step = Math.max(1e6, Math.ceil((r.end - r.start) / 30000 / 1e6) * 1e6);
  const times: number[] = [];
  for (let t = Math.ceil(Math.max(from, r.start) / step) * step; t <= Math.min(to, r.end); t += step) {
    if (!r.gaps.some((g) => t > g.start && t < g.end)) {
      times.push(t);
    }
  }
  return query(h, from, to, width, fields, times, r.gaps);
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
  }, 1000);
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
    const h: MockHost = { status: status(id, conn.addr, conn.user, "disconnected"), devices: null, interval: 1000, lastAgo: 0, seed: 6 };
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
  async query(id, from, to, width, fields) {
    return query(find(id), from, to, width, fields);
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
  async getSettings() {
    return structuredClone(settings);
  },
  async choices() {
    return choices;
  },
  async setAppearance() {},
  async setLanguage() {},
  async setMonitorRange() {},
  async setHistoryRange() {},
  async setCurves() {},
  async setGPUCollapsed() {},
  async setInterval(ms) {
    settings.interval_ms = ms;
    for (const h of hosts) {
      if (h.status.state === "connected") {
        h.interval = ms;
      }
    }
  },
  async setProcessesCollapsed(hostID, collapsed) {
    const rest = (settings.collapsed_procs ?? []).filter((id) => id !== hostID);
    settings.collapsed_procs = collapsed ? [...rest, hostID].sort() : rest;
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
