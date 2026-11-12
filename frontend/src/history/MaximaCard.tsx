import { memo } from "react";
import type { Device, Field, FieldMax, RecordingMaxima, SizeUnits } from "../api/types";
import { ChevronDown, ChevronRight } from "../components/Icons";
import { memGiga, memGigaKey } from "../format";
import { useT, type MessageKey, type Translator } from "../i18n";
import { useSizeUnits, type MaxSelection } from "../state/app";

// maxColumns 為最大值卡片的欄位, 順序與名稱同 GPU 卡片的文字讀數. legend 為曲線圖例中
// 該項目的名稱.
export const maxColumns: { field: Field; label: MessageKey; legend: MessageKey }[] = [
  { field: "util", label: "readout.util", legend: "legend.util" },
  { field: "mem_used", label: "readout.mem", legend: "legend.mem" },
  { field: "temp", label: "readout.temp", legend: "legend.temp" },
  { field: "fan", label: "readout.fan", legend: "legend.fan" },
  { field: "power_w", label: "readout.power", legend: "legend.power" },
  { field: "clock_gpu", label: "readout.clockGpu", legend: "legend.clock_gpu" },
  { field: "clock_mem", label: "readout.clockMem", legend: "legend.clock_mem" },
];

// findMax 回傳最大值結果中某張 GPU 某個欄位的最大值, 沒有時為 undefined.
export function findMax(maxima: RecordingMaxima | null | undefined, gpu: number, field: Field): FieldMax | undefined {
  return maxima?.gpus.find((g) => g.id === gpu)?.fields[field];
}

// maxValueText 回傳最大值的文字, 格式與取整同 GPU 卡片的文字讀數. 記憶體另回傳佔比,
// 取不到記憶體總量時為 null; 功耗不含上限.
function maxValueText(field: Field, v: number, device: Device, units: SizeUnits, t: Translator): { text: string; pct: string | null } {
  const n = Math.round(v);
  switch (field) {
    case "util":
    case "fan":
      return { text: t("unit.percent", { value: n }), pct: null };
    case "mem_used": {
      const total = device.mem_total;
      return {
        text: t(memGigaKey(units), { value: memGiga(n, units) }),
        pct: total ? t("unit.percent", { value: Math.round((n / total) * 100) }) : null,
      };
    }
    case "temp":
      return { text: t("unit.celsius", { value: n }), pct: null };
    case "power_w":
      return { text: t("unit.watt", { value: n }), pct: null };
    default:
      return { text: t("unit.mhz", { value: n }), pct: null };
  }
}

export interface MaximaCardProps {
  gpus: Device[];
  // maxima 為整個記錄檔的最大值; undefined 為查詢中, null 為查詢失敗.
  maxima: RecordingMaxima | null | undefined;
  sel: MaxSelection | null;
  collapsed: boolean;
  onToggle: (collapsed: boolean) => void;
  // onPick 於點選數值或切換出現處時回報新的選取與該處的時刻.
  onPick: (sel: MaxSelection, t: number) => void;
  // timeText 回傳時刻的文字, 格式同固定時刻標籤.
  timeText: (us: number) => string;
}

// MaximaCard 為歷史畫面最上方的最大值卡片, 每張 GPU 一列, 列出各欄位於整個記錄檔的
// 最大值. 數值為按鍵, 點選即把固定時刻設為該數值第一處出現的時刻. 標題列右側於未選取時
// 為操作說明, 選取時為選取的項目, 出現處的序號與切換出現處的按鍵; 卡片收合時照常顯示.
export const MaximaCard = memo(function MaximaCard({ gpus, maxima, sel, collapsed, onToggle, onPick, timeText }: MaximaCardProps) {
  const t = useT();
  const units = useSizeUnits();

  const selMax = sel ? findMax(maxima, sel.gpu, sel.field) : undefined;
  const selDevice = sel ? gpus.find((g) => g.id === sel.gpu) : undefined;
  let status;
  if (sel && selMax && selDevice) {
    const col = maxColumns.find((c) => c.field === sel.field);
    const v = maxValueText(sel.field, selMax.value, selDevice, units, t);
    const n = selMax.runs.length;
    const params = {
      gpu: t("monitor.gpu", { id: sel.gpu }),
      name: col ? t(col.label) : sel.field,
      value: v.pct === null ? v.text : `${v.text} ${v.pct}`,
      index: sel.index + 1,
      count: n,
    };
    const go = (k: number) => onPick({ ...sel, index: k }, selMax.runs[k][0]);
    status = (
      <span className="max-status on">
        <span className="ellipsis">{t(n === 1 ? "history.maxSelOnce" : "history.maxSelAt", params)}</span>
        {n > 1 && (
          <>
            <button
              type="button"
              className="max-nav"
              title={t("history.maxPrev")}
              aria-label={t("history.maxPrev")}
              disabled={sel.index <= 0}
              onClick={() => go(sel.index - 1)}
            >
              ‹
            </button>
            <button
              type="button"
              className="max-nav"
              title={t("history.maxNext")}
              aria-label={t("history.maxNext")}
              disabled={sel.index >= n - 1}
              onClick={() => go(sel.index + 1)}
            >
              ›
            </button>
          </>
        )}
      </span>
    );
  } else {
    status = (
      <span className="max-status">
        <span className="ellipsis">{t("history.maxHint")}</span>
      </span>
    );
  }

  const head = (
    <div className="procs-head">
      <button type="button" className="card-title" aria-expanded={!collapsed} onClick={() => onToggle(!collapsed)}>
        {collapsed ? <ChevronRight /> : <ChevronDown />}
        <span className="ellipsis">{t("history.maxTitle")}</span>
      </button>
      {status}
    </div>
  );

  if (collapsed) {
    return <section className="card card-collapsed max-card">{head}</section>;
  }

  const cell = (g: Device, field: Field) => {
    if (maxima === undefined) {
      return <td key={field} className="num" />;
    }
    const m = findMax(maxima, g.id, field);
    if (!m || m.runs.length === 0) {
      return (
        <td key={field} className="num na">
          {t("value.na")}
        </td>
      );
    }
    const v = maxValueText(field, m.value, g, units, t);
    const on = sel !== null && sel.gpu === g.id && sel.field === field;
    const first = timeText(m.runs[0][0]);
    const title =
      m.runs.length === 1 ? t("history.maxAt", { time: first }) : t("history.maxFirstAt", { time: first, count: m.runs.length });
    return (
      <td key={field} className="num max-td">
        <button
          type="button"
          className={on ? "max-cell sel" : "max-cell"}
          title={title}
          aria-pressed={on}
          onClick={() => onPick({ gpu: g.id, field, index: 0 }, m.runs[0][0])}
        >
          {v.text}
          {v.pct !== null && (
            <>
              {" "}
              <span className="max-pct">{v.pct}</span>
            </>
          )}
        </button>
      </td>
    );
  };

  return (
    <section className="card procs max-card">
      {head}
      <div className="max-scroll">
        <table className="procs-table max-table">
          <thead>
            <tr>
              <th scope="col">{t("proc.gpu")}</th>
              {maxColumns.map((c) => (
                <th key={c.field} scope="col" className="num">
                  {t(c.label)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {gpus.map((g) => (
              <tr key={g.id}>
                <td>
                  <span className="gpu-badge">{t("monitor.gpu", { id: g.id })}</span>
                </td>
                {maxColumns.map((c) => cell(g, c.field))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
});
