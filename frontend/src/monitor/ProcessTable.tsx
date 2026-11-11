import { memo, useMemo, useState } from "react";
import { createPortal } from "react-dom";
import type { Device, LiveProcess, ProcColumn, ProcFilter, ProcSort } from "../api/types";
import { ChevronDown, ChevronRight, CloseIcon, FilterIcon, SortIcon } from "../components/Icons";
import { ProcessFilterDialog } from "../dialogs/ProcessFilterDialog";
import { bytesUnit } from "../format";
import { useT, type MessageKey } from "../i18n";
import { defaultProcSort, procFilterEmpty, setProcessFilter, setProcessSort, settingsStore, useSizeUnits } from "../state/app";
import { useStore } from "../state/store";

// columns 為表格的欄位, 依顯示順序排列. num 為數值欄, 靠右對齊.
const columns: { id: ProcColumn; label: MessageKey; num?: boolean; className?: string }[] = [
  { id: "gpu", label: "proc.gpu" },
  { id: "pid", label: "proc.pid", num: true },
  { id: "user", label: "proc.user" },
  { id: "mem", label: "proc.mem", num: true },
  { id: "command", label: "proc.cmd", className: "col-cmd" },
];

function compareText(a: string, b: string): number {
  const x = a.toLowerCase();
  const y = b.toLowerCase();
  return x < y ? -1 : x > y ? 1 : 0;
}

// sortProcs 回傳依 sort 排序後的新陣列. 數值相同時依序以 GPU 編號, PID 遞增決定
// 先後; GPU MEM 為 N/A 的程序不論方向一律排在最後. USER 與 COMMAND 不分大小寫.
function sortProcs(procs: LiveProcess[], sort: ProcSort): LiveProcess[] {
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
function filterProcs(procs: LiveProcess[], f: ProcFilter | undefined): LiveProcess[] {
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

// ProcessTable 為一台主機的程序卡片. hostID 為排序方式與過濾條件所屬主機的識別字串,
// gpus 為過濾視窗列出的 GPU. title 由呼叫端提供, 例如重新連線中時附上最後收到的
// 時刻. emptyText 為沒有程序時的說明, 省略時用監看畫面的說明. 點選標題切換收合,
// 收合後只留標題列, 標題列的過濾條件標籤與 "Filter" 按鍵照常可用. 按下欄位名稱
// 依該欄排序, 再按一次反轉方向. 表格內容區的高度固定, 程序超過 5 個時於卡片內
// 上下捲動. 監看畫面與歷史畫面共用.
export const ProcessTable = memo(function ProcessTable({
  hostID,
  gpus,
  title,
  procs,
  emptyText,
  collapsed,
  onToggle,
}: {
  hostID: string;
  gpus: Device[];
  title: string;
  procs: LiveProcess[];
  emptyText?: string;
  collapsed: boolean;
  onToggle: (collapsed: boolean) => void;
}) {
  const t = useT();
  const sort = useStore(settingsStore, (s) => s?.proc_sorts?.[hostID]) ?? defaultProcSort;
  const filter = useStore(settingsStore, (s) => s?.proc_filters?.[hostID]);
  const units = useSizeUnits();
  const [filtering, setFiltering] = useState(false);
  const shown = useMemo(() => sortProcs(filterProcs(procs, filter), sort), [procs, filter, sort]);

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
  return (
    <section className="card procs">
      {head}
      <div className="procs-scroll">
        <table className="procs-table">
          <thead>
            <tr>
              {columns.map((c) => {
                const on = sort.column === c.id;
                const cls = [c.num ? "num" : "", c.className ?? ""].filter(Boolean).join(" ");
                return (
                  <th
                    key={c.id}
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
                <td colSpan={5} className="procs-empty">
                  {procs.length === 0 ? (emptyText ?? t("monitor.noProcesses")) : t("proc.noMatch")}
                </td>
              </tr>
            )}
            {shown.map((p) => {
              const mem = p.mem === null ? null : bytesUnit(p.mem, units);
              return (
                <tr key={`${p.gpu}-${p.pid}`}>
                  <td>
                    <span className="gpu-badge">{t("monitor.gpu", { id: p.gpu })}</span>
                  </td>
                  <td className="num">{p.pid}</td>
                  <td>
                    <span className="user" title={p.user}>
                      {p.user}
                    </span>
                  </td>
                  <td className={`num${mem ? "" : " na"}`}>
                    {mem ? t(mem.key, { value: mem.value }) : t("value.na")}
                  </td>
                  <td className="cmd" title={p.cmd}>
                    {p.cmd}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {dialog}
    </section>
  );
});
