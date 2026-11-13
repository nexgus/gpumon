import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import { createPortal } from "react-dom";
import type { Device, LiveProcess, ProcColumn, ProcFilter, ProcSort } from "../api/types";
import { ChevronDown, ChevronRight, CloseIcon, FilterIcon, SortIcon } from "../components/Icons";
import { ProcessFilterDialog } from "../dialogs/ProcessFilterDialog";
import { bytesUnit } from "../format";
import { useT, type MessageKey } from "../i18n";
import {
  defaultProcSort,
  pinsStore,
  procFilterEmpty,
  refreshPinnedUsers,
  samePinned,
  setPinned,
  setProcessFilter,
  setProcessSort,
  settingsStore,
  useSizeUnits,
  type PinnedProc,
} from "../state/app";
import { useStore } from "../state/store";

// columns 為表格的欄位, 依顯示順序排列. num 為數值欄, 靠右對齊.
const columns: { id: ProcColumn; label: MessageKey; num?: boolean; className?: string }[] = [
  { id: "gpu", label: "proc.gpu" },
  { id: "pid", label: "proc.pid", num: true },
  { id: "user", label: "proc.user" },
  { id: "util", label: "proc.util", num: true },
  { id: "mem", label: "proc.mem", num: true },
  { id: "command", label: "proc.cmd", className: "col-cmd" },
];

function compareText(a: string, b: string): number {
  const x = a.toLowerCase();
  const y = b.toLowerCase();
  return x < y ? -1 : x > y ? 1 : 0;
}

// sortProcs 回傳依 sort 排序後的新陣列. 數值相同時依序以 GPU 編號, PID 遞增決定
// 先後; GPU UTIL 沒有數值或 GPU MEM 為 N/A 的程序不論方向一律排在最後. USER 與 COMMAND 不分大小寫.
function sortProcs<T extends LiveProcess>(procs: T[], sort: ProcSort): T[] {
  const dir = sort.order === "desc" ? -1 : 1;
  const primary = (a: LiveProcess, b: LiveProcess): number => {
    switch (sort.column) {
      case "gpu":
        return dir * (a.gpu - b.gpu);
      case "pid":
        return dir * (a.pid - b.pid);
      case "user":
        return dir * compareText(a.user, b.user);
      case "command":
        return dir * compareText(a.cmd, b.cmd);
      case "util": {
        const x = a.util ?? null;
        const y = b.util ?? null;
        if (x === null || y === null) {
          return x === y ? 0 : x === null ? 1 : -1;
        }
        return dir * (x - y);
      }
      case "mem":
        if (a.mem === null || b.mem === null) {
          return a.mem === b.mem ? 0 : a.mem === null ? 1 : -1;
        }
        return dir * (a.mem - b.mem);
    }
  };
  return [...procs].sort((a, b) => primary(a, b) || a.gpu - b.gpu || a.pid - b.pid);
}

// filterProcs 回傳符合全部條件的程序. 未設定的條件不參與過濾; 文字條件為部分相符,
// 不分大小寫.
function filterProcs<T extends LiveProcess>(procs: T[], f: ProcFilter | undefined): T[] {
  if (procFilterEmpty(f) || !f) {
    return procs;
  }
  const gpus = f.gpus && f.gpus.length > 0 ? new Set(f.gpus) : null;
  const user = f.user.toLowerCase();
  const command = f.command.toLowerCase();
  return procs.filter(
    (p) =>
      (!gpus || gpus.has(p.gpu)) &&
      (user === "" || p.user.toLowerCase().includes(user)) &&
      (command === "" || p.cmd.toLowerCase().includes(command)),
  );
}

// FilterChip 為一項過濾條件的標籤; 按下 ✕ 立即清除該條件.
function FilterChip({ name, value, onRemove }: { name: string; value: string; onRemove: () => void }) {
  const t = useT();
  return (
    <span className="filter-chip">
      <span className="filter-chip-name">{name}</span>
      <span className="filter-chip-value mono" title={value}>
        {value}
      </span>
      <button
        type="button"
        className="filter-chip-remove"
        aria-label={t("proc.removeFilter")}
        title={t("proc.removeFilter")}
        onClick={onRemove}
      >
        <CloseIcon />
      </button>
    </span>
  );
}

// Row 為表格的一列. pinned 為置頂的列; missing 為置頂而不在傳入的程序清單中的列, 其使用者與
// 指令為置頂期間最後一次取得的內容, 沒有用量.
interface Row extends LiveProcess {
  key: string;
  pinned: boolean;
  missing: boolean;
}

const noPins: PinnedProc[] = [];

// 方框的切換由所在的列處理 (含鍵盤操作時由方框送出的點擊事件), 方框本身不處理變更.
const noop = () => {};

function rowKey(p: { gpu: number; pid: number; cmd: string }): string {
  return `${p.gpu}\u0000${p.pid}\u0000${p.cmd}`;
}

// buildRows 回傳過濾與排序後的列. 置頂的列在前, 未置頂的列在後, 兩者各自依 sort 排列;
// 依 GPU UTIL 或 GPU MEM 排序時, 不在清單中的置頂列不論方向皆排在置頂的列的最後.
function buildRows(procs: LiveProcess[], pins: PinnedProc[], filter: ProcFilter | undefined, sort: ProcSort): Row[] {
  const rows: Row[] = procs.map((p) => ({ ...p, key: rowKey(p), pinned: pins.some((x) => samePinned(x, p)), missing: false }));
  for (const x of pins) {
    if (!procs.some((p) => samePinned(x, p))) {
      rows.push({ gpu: x.gpu, pid: x.pid, user: x.user, cmd: x.cmd, mem: null, util: null, key: rowKey(x), pinned: true, missing: true });
    }
  }
  const shown = filterProcs(rows, filter);
  const pinned = shown.filter((r) => r.pinned);
  const usage = sort.column === "util" || sort.column === "mem";
  const top = usage
    ? [...sortProcs(pinned.filter((r) => !r.missing), sort), ...sortProcs(pinned.filter((r) => r.missing), sort)]
    : sortProcs(pinned, sort);
  return [...top, ...sortProcs(shown.filter((r) => !r.pinned), sort)];
}

// Measured 為一段指令的字元與各字元左緣的位置 (像素); prefix 比 chars 多一項, 末項為全長.
interface Measured {
  chars: string[];
  prefix: number[];
}

// 指令的寬度以 canvas 依指令欄的字型量測, 依字型與指令快取. 字型載入完成後清除快取.
let measureCtx: CanvasRenderingContext2D | null = null;
const measureCache = new Map<string, Map<string, Measured>>();
const measureCacheLimit = 4000;

function measureText(font: string, text: string): Measured {
  let byFont = measureCache.get(font);
  if (!byFont) {
    byFont = new Map();
    measureCache.set(font, byFont);
  }
  const hit = byFont.get(text);
  if (hit) {
    return hit;
  }
  if (!measureCtx) {
    measureCtx = document.createElement("canvas").getContext("2d");
  }
  const chars = Array.from(text);
  const prefix = [0];
  if (measureCtx) {
    measureCtx.font = font;
    for (const c of chars) {
      prefix.push(prefix[prefix.length - 1] + measureCtx.measureText(c).width);
    }
  }
  if (byFont.size >= measureCacheLimit) {
    byFont.clear();
  }
  const m = { chars, prefix };
  byFont.set(text, m);
  return m;
}

const ellipsis = "...";

// clipCommand 回傳指令於捲動位置 x (像素) 時顯示的文字. 可完整顯示的指令原樣回傳; 否則
// 該列捲動至指令結尾即停止, 被截斷的一端以省略號取代, 只顯示完整落在欄內的字元, 不露出
// 被裁切的殘字. inner 為指令欄可放置文字的寬度, ell 為省略號的寬度.
function clipCommand(m: Measured, inner: number, x: number, ell: number): string {
  const total = m.prefix[m.chars.length];
  if (total <= inner) {
    return m.chars.join("");
  }
  const end = total - inner;
  let ox = Math.min(Math.max(x, 0), end);
  if (ox < 0.5) {
    ox = 0;
  } else if (ox > end - 0.5) {
    ox = end;
  }
  const left = ox > 0;
  const right = ox < end;
  const from = ox + (left ? ell : 0);
  const to = ox + inner - (right ? ell : 0);
  if (to < from) {
    return ell <= inner ? ellipsis : "";
  }
  const eps = 0.01;
  let i = 0;
  while (i < m.chars.length && m.prefix[i] < from - eps) {
    i++;
  }
  let j = i;
  while (j < m.chars.length && m.prefix[j + 1] <= to + eps) {
    j++;
  }
  return (left ? ellipsis : "") + m.chars.slice(i, j).join("") + (right ? ellipsis : "");
}

// Geo 為指令欄的位置與尺寸 (像素): left 為欄左緣相對於表格內容區左緣的距離, width 為欄寬,
// inner 為扣除左右留白並保留 1 像素餘裕後可放置文字的寬度, font 為指令欄的字型.
interface Geo {
  left: number;
  width: number;
  inner: number;
  font: string;
}

function sameGeo(a: Geo | null, b: Geo): boolean {
  return !!a && a.left === b.left && a.width === b.width && a.inner === b.inner && a.font === b.font;
}

// dragSlop 為按下與放開滑鼠之間的位移上限 (像素), 超過即視為拖曳.
const dragSlop = 4;

// wheelLine 為滾輪以行為單位時每行換算的像素.
const wheelLine = 16;

// ProcessTable 為一台主機的程序卡片. hostID 為排序方式, 過濾條件與置頂所屬主機的識別字串,
// gpus 為過濾視窗列出的 GPU. title 由呼叫端提供, 例如重新連線中時附上最後收到的
// 時刻. emptyText 為沒有程序時的說明, missingText 為置頂的程序不在 procs 中時的說明,
// 兩者省略時皆用監看畫面的說明. 點選標題切換收合,
// 收合後只留標題列, 標題列的過濾條件標籤與 "Filter" 按鍵照常可用. 按下欄位名稱
// 依該欄排序, 再按一次反轉方向. 點擊列上任何位置 (含第一欄的方框) 切換該列的置頂, 置頂的列排在最前; 置頂的
// 程序不在 procs 中時, 該列仍以記下的內容顯示 missingText. 表格內容區隨卡片伸縮,
// 最小為 5.5 列高, 列數超出時於卡片內上下捲動. 指令超出欄寬時只有指令欄左右捲動,
// 各列共用同一個捲動位置, 捲軸位於指令欄的正下方. 監看畫面與歷史畫面共用.
export const ProcessTable = memo(function ProcessTable({
  hostID,
  gpus,
  title,
  procs,
  emptyText,
  missingText,
  collapsed,
  onToggle,
}: {
  hostID: string;
  gpus: Device[];
  title: string;
  procs: LiveProcess[];
  emptyText?: string;
  missingText?: string;
  collapsed: boolean;
  onToggle: (collapsed: boolean) => void;
}) {
  const t = useT();
  const sort = useStore(settingsStore, (s) => s?.proc_sorts?.[hostID]) ?? defaultProcSort;
  const filter = useStore(settingsStore, (s) => s?.proc_filters?.[hostID]);
  const pins = useStore(pinsStore, (s) => s[hostID]) ?? noPins;
  const units = useSizeUnits();
  const [filtering, setFiltering] = useState(false);
  const shown = useMemo(() => buildRows(procs, pins, filter, sort), [procs, pins, filter, sort]);

  useEffect(() => refreshPinnedUsers(hostID, procs), [hostID, procs, pins]);

  // 按下滑鼠時的位置; 放開時位移超過 dragSlop 像素視為拖曳反白文字, 不切換置頂.
  const downRef = useRef<{ x: number; y: number } | null>(null);
  const onRowClick = (p: Row, e: MouseEvent<HTMLTableRowElement>) => {
    const down = downRef.current;
    downRef.current = null;
    if (down && e.detail > 0 && Math.hypot(e.clientX - down.x, e.clientY - down.y) > dragSlop) {
      return;
    }
    setPinned(hostID, p, !p.pinned);
  };

  const scrollRef = useRef<HTMLDivElement>(null);
  const cmdHeadRef = useRef<HTMLTableCellElement>(null);
  const barRef = useRef<HTMLDivElement>(null);
  const collapsedRef = useRef(collapsed);
  const [geo, setGeo] = useState<Geo | null>(null);
  const [scrollX, setScrollX] = useState(0);
  const [fontEpoch, setFontEpoch] = useState(0);

  // measure 讀取指令欄的位置, 寬度與字型; 沒有指令欄 (卡片收合) 時不變.
  const measure = useCallback(() => {
    const scroll = scrollRef.current;
    const th = cmdHeadRef.current;
    if (!scroll || !th) {
      return;
    }
    const sr = scroll.getBoundingClientRect();
    const hr = th.getBoundingClientRect();
    const hs = getComputedStyle(th);
    const td = scroll.querySelector("td.cmd");
    const cs = td ? getComputedStyle(td) : null;
    const font = cs ? `${cs.fontStyle} ${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}` : "";
    setGeo((g) => {
      // 沒有任何列時讀不到指令欄的字型, 沿用先前讀到的字型.
      const next: Geo = {
        left: hr.left - sr.left,
        width: hr.width,
        inner: Math.max(0, Math.floor(hr.width - parseFloat(hs.paddingLeft) - parseFloat(hs.paddingRight)) - 1),
        font: font || (g?.font ?? ""),
      };
      return sameGeo(g, next) ? g : next;
    });
  }, []);

  useLayoutEffect(measure);

  useEffect(() => {
    const scroll = scrollRef.current;
    const th = cmdHeadRef.current;
    if (collapsed || !scroll || !th) {
      return;
    }
    const ro = new ResizeObserver(measure);
    ro.observe(scroll);
    ro.observe(th);
    return () => ro.disconnect();
  }, [collapsed, measure]);

  useEffect(() => {
    const fonts = document.fonts;
    if (!fonts) {
      return;
    }
    const onLoad = () => {
      measureCache.clear();
      setFontEpoch((n) => n + 1);
    };
    fonts.addEventListener("loadingdone", onLoad);
    return () => fonts.removeEventListener("loadingdone", onLoad);
  }, []);

  // 各列指令的量測結果, 以及省略號的寬度與捲動範圍.
  const clip = useMemo(() => {
    if (!geo || geo.font === "") {
      return null;
    }
    const ms = shown.map((r) => measureText(geo.font, r.cmd));
    const ell = measureText(geo.font, ellipsis).prefix[ellipsis.length];
    let range = 0;
    for (const m of ms) {
      range = Math.max(range, m.prefix[m.chars.length] - geo.inner);
    }
    return { ms, ell, range: Math.ceil(range) };
    // fontEpoch 於字型載入完成時改變, 使量測結果重新計算.
  }, [shown, geo, fontEpoch]);
  const range = clip?.range ?? 0;
  const x = Math.min(scrollX, range);

  // 捲軸為水平位置的唯一來源, 畫面只讀取其位置, 不於每次繪製後寫回; 寫回會把過時的
  // 位置蓋過使用者拖曳的結果. 唯一的例外是卡片由收合恢復展開, 捲軸重新建立時, 把
  // 先前的位置設定回捲軸一次. 捲動範圍變小時把記住的位置收回至範圍內; 捲軸本身由
  // 瀏覽器收回.
  useLayoutEffect(() => {
    const wasCollapsed = collapsedRef.current;
    collapsedRef.current = collapsed;
    if (scrollX > range) {
      setScrollX(range);
    }
    const bar = barRef.current;
    if (wasCollapsed && !collapsed && bar) {
      bar.scrollLeft = x;
    }
  });

  // 於表格上以觸控板水平捲動或 Shift 加滾輪時捲動指令欄; 上下方向的滾輪維持原行為.
  useEffect(() => {
    const scroll = scrollRef.current;
    if (collapsed || !scroll) {
      return;
    }
    const onWheel = (e: WheelEvent) => {
      const bar = barRef.current;
      let dx = e.deltaX;
      if (e.shiftKey && dx === 0) {
        dx = e.deltaY;
      } else if (Math.abs(dx) <= Math.abs(e.deltaY)) {
        return;
      }
      if (!bar || dx === 0 || bar.scrollWidth <= bar.clientWidth) {
        return;
      }
      if (e.deltaMode === WheelEvent.DOM_DELTA_LINE) {
        dx *= wheelLine;
      } else if (e.deltaMode === WheelEvent.DOM_DELTA_PAGE) {
        dx *= bar.clientWidth;
      }
      bar.scrollLeft += dx;
      e.preventDefault();
    };
    scroll.addEventListener("wheel", onWheel, { passive: false });
    return () => scroll.removeEventListener("wheel", onWheel);
  }, [collapsed]);

  const active = !procFilterEmpty(filter);
  const chips: { key: string; name: string; value: string; clear: Partial<ProcFilter> }[] = [];
  if (filter && filter.gpus && filter.gpus.length > 0) {
    chips.push({ key: "gpu", name: t("proc.gpu"), value: filter.gpus.join(", "), clear: { gpus: [] } });
  }
  if (filter?.user) {
    chips.push({ key: "user", name: t("proc.user"), value: filter.user, clear: { user: "" } });
  }
  if (filter?.command) {
    chips.push({ key: "command", name: t("proc.cmd"), value: filter.command, clear: { command: "" } });
  }

  const head = (
    <div className="procs-head">
      <button
        type="button"
        className="card-title"
        aria-expanded={!collapsed}
        onClick={() => onToggle(!collapsed)}
      >
        {collapsed ? <ChevronRight /> : <ChevronDown />}
        <span className="ellipsis">{title}</span>
      </button>
      {chips.length > 0 && (
        <div className="filter-chips">
          {chips.map((c) => (
            <FilterChip
              key={c.key}
              name={c.name}
              value={c.value}
              onRemove={() => filter && void setProcessFilter(hostID, { ...filter, ...c.clear })}
            />
          ))}
        </div>
      )}
      <button
        type="button"
        className={`btn btn-sm procs-filter${active ? " btn-on" : ""}`}
        onClick={() => setFiltering(true)}
      >
        <FilterIcon />
        {t("proc.filter")}
      </button>
    </div>
  );

  const dialog =
    filtering &&
    createPortal(
      <ProcessFilterDialog
        gpus={gpus}
        filter={filter}
        onApply={(f) => void setProcessFilter(hostID, f)}
        onClose={() => setFiltering(false)}
      />,
      document.body,
    );

  if (collapsed) {
    return (
      <section className="card card-collapsed">
        {head}
        {dialog}
      </section>
    );
  }
  const cols = columns.length + 1;
  return (
    <section className="card procs">
      {head}
      <div className="procs-scroll" ref={scrollRef}>
        <table className="procs-table">
          <thead>
            <tr>
              <th scope="col" className="col-pin">
                {t("proc.pin")}
              </th>
              {columns.map((c) => {
                const on = sort.column === c.id;
                const cls = [c.num ? "num" : "", c.className ?? ""].filter(Boolean).join(" ");
                return (
                  <th
                    key={c.id}
                    ref={c.id === "command" ? cmdHeadRef : undefined}
                    scope="col"
                    className={cls || undefined}
                    aria-sort={on ? (sort.order === "asc" ? "ascending" : "descending") : undefined}
                  >
                    <button
                      type="button"
                      className={`sort-btn${on ? " on" : ""}`}
                      onClick={() =>
                        void setProcessSort(hostID, {
                          column: c.id,
                          order: on && sort.order === "asc" ? "desc" : "asc",
                        })
                      }
                    >
                      {t(c.label)}
                      <SortIcon up={on && sort.order === "asc"} />
                    </button>
                  </th>
                );
              })}
            </tr>
          </thead>
          <tbody>
            {shown.length === 0 && (
              <tr>
                <td colSpan={cols} className="procs-empty">
                  {procs.length === 0 && pins.length === 0
                    ? (emptyText ?? t("monitor.noProcesses"))
                    : t("proc.noMatch")}
                </td>
              </tr>
            )}
            {shown.map((p, i) => {
              const mem = p.mem === null ? null : bytesUnit(p.mem, units);
              const cls = ["row", p.pinned ? "pinned" : "", p.missing ? "missing" : ""].filter(Boolean).join(" ");
              return (
                <tr
                  key={p.key}
                  className={cls}
                  onMouseDown={(e) => {
                    downRef.current = { x: e.clientX, y: e.clientY };
                  }}
                  onClick={(e) => onRowClick(p, e)}
                >
                  <td className="pin">
                    <input
                      type="checkbox"
                      className="check"
                      checked={p.pinned}
                      aria-label={t("proc.pin")}
                      onChange={noop}
                    />
                  </td>
                  <td>
                    <span className="gpu-badge">{t("monitor.gpu", { id: p.gpu })}</span>
                  </td>
                  <td className="num">{p.pid}</td>
                  <td>
                    <span className="user" title={p.user}>
                      {p.user}
                    </span>
                  </td>
                  {p.missing ? (
                    <td colSpan={2} className="not-running">
                      {missingText ?? t("monitor.processExited")}
                    </td>
                  ) : (
                    <>
                      <td className={`num${p.util == null ? " na" : ""}`}>{p.util == null ? "-" : `${p.util}%`}</td>
                      <td className={`num${mem ? "" : " na"}`}>
                        {mem ? t(mem.key, { value: mem.value }) : t("value.na")}
                      </td>
                    </>
                  )}
                  <td className="cmd" title={p.cmd}>
                    {clip && geo ? clipCommand(clip.ms[i], geo.inner, x, clip.ell) : p.cmd}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <div
        className="procs-hbar"
        ref={barRef}
        style={{
          marginLeft: geo?.left ?? 0,
          width: geo?.width ?? 0,
          visibility: range > 0 ? "visible" : "hidden",
        }}
        onScroll={(e) => setScrollX(e.currentTarget.scrollLeft)}
      >
        <div className="procs-hbar-fill" style={{ width: (geo?.width ?? 0) + range }} />
      </div>
      {dialog}
    </section>
  );
});
