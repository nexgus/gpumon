import { memo } from "react";
import type { LiveProcess } from "../api/types";
import { bytesUnit } from "../format";
import { useT } from "../i18n";

// ProcessTable 為一台主機的程序列表. title 由呼叫端提供, 例如重新連線中時附上最後
// 收到的時刻. emptyText 為沒有程序時的說明, 省略時用監看畫面的說明. 監看畫面與歷史
// 畫面共用.
export const ProcessTable = memo(function ProcessTable({
  title,
  procs,
  emptyText,
}: {
  title: string;
  procs: LiveProcess[];
  emptyText?: string;
}) {
  const t = useT();
  return (
    <section className="procs">
      <div className="procs-title">{title}</div>
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
              <th scope="col">{t("proc.cmd")}</th>
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
                  <td>{p.gpu}</td>
                  <td className="num">{p.pid}</td>
                  <td>{p.user}</td>
                  <td className={`num${mem ? "" : " na"}`}>
                    {mem ? t(mem.unit === "gib" ? "unit.gib" : "unit.mib", { value: mem.value }) : t("value.na")}
                  </td>
                  <td className="cmd">{p.cmd}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
});
