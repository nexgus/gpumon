import { memo } from "react";
import type { LiveProcess } from "../api/types";
import { ChevronDown, ChevronRight } from "../components/Icons";
import { bytesUnit } from "../format";
import { useT } from "../i18n";

// ProcessTable 為一台主機的程序卡片. title 由呼叫端提供, 例如重新連線中時附上最後
// 收到的時刻. emptyText 為沒有程序時的說明, 省略時用監看畫面的說明. 點選標題切換
// 收合, 收合後只留標題一行. 表格內容區的高度固定, 程序超過 5 個時於卡片內上下
// 捲動. 監看畫面與歷史畫面共用.
export const ProcessTable = memo(function ProcessTable({
  title,
  procs,
  emptyText,
  collapsed,
  onToggle,
}: {
  title: string;
  procs: LiveProcess[];
  emptyText?: string;
  collapsed: boolean;
  onToggle: (collapsed: boolean) => void;
}) {
  const t = useT();
  const head = (
    <button
      type="button"
      className="card-title procs-title"
      aria-expanded={!collapsed}
      onClick={() => onToggle(!collapsed)}
    >
      {collapsed ? <ChevronRight /> : <ChevronDown />}
      <span className="ellipsis">{title}</span>
    </button>
  );
  if (collapsed) {
    return <section className="card card-collapsed">{head}</section>;
  }
  return (
    <section className="card procs">
      {head}
      <div className="procs-scroll">
        <table className="procs-table">
          <thead>
            <tr>
              <th scope="col">{t("proc.gpu")}</th>
              <th scope="col" className="num">
                {t("proc.pid")}
              </th>
              <th scope="col">{t("proc.user")}</th>
              <th scope="col" className="num">
                {t("proc.mem")}
              </th>
              <th scope="col" className="col-cmd">
                {t("proc.cmd")}
              </th>
            </tr>
          </thead>
          <tbody>
            {procs.length === 0 && (
              <tr>
                <td colSpan={5} className="procs-empty">
                  {emptyText ?? t("monitor.noProcesses")}
                </td>
              </tr>
            )}
            {procs.map((p) => {
              const mem = p.mem === null ? null : bytesUnit(p.mem);
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
                    {mem ? t(mem.unit === "gib" ? "unit.gib" : "unit.mib", { value: mem.value }) : t("value.na")}
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
    </section>
  );
});
