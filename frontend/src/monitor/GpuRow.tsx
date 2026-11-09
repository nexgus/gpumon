import { memo } from "react";
import type { Device, LiveGPU } from "../api/types";
import { Chart, ChartLegend, type ChartData, type ChartSeries } from "../chart/Chart";
import { ChevronDown, ChevronRight } from "../components/Icons";
import { gib } from "../format";
import { useT, type Translator } from "../i18n";

function Bar({ pct, color }: { pct: number; color: string }) {
  return (
    <div className="bar">
      <div className="bar-fill" style={{ width: `${Math.max(0, Math.min(100, pct))}%`, background: `var(${color})` }} />
    </div>
  );
}

function Value({ text, big }: { text: string | null; big?: boolean }) {
  const t = useT();
  return <span className={`${big ? "readout-big" : "readout-value"}${text === null ? " na" : ""}`}>{text ?? t("value.na")}</span>;
}

function memText(used: number | null, total: number | null | undefined, t: Translator): string | null {
  if (used === null) {
    return null;
  }
  if (!total) {
    return t("unit.gib", { value: gib(used) });
  }
  return t("value.pair", { used: gib(used), total: t("unit.gib", { value: gib(total) }) });
}

// Readouts 為 GPU 卡片左側的文字讀數. gpu 為 undefined 時 (尚無取樣) 全部顯示 N/A.
const Readouts = memo(function Readouts({ device, gpu }: { device: Device; gpu: LiveGPU | undefined }) {
  const t = useT();
  const v = (x: number | null | undefined) => (x === null || x === undefined ? null : x);
  const util = v(gpu?.util);
  const used = v(gpu?.mem_used);
  const total = device.mem_total;
  const temp = v(gpu?.temp);
  const fan = v(gpu?.fan);
  const power = v(gpu?.power_w);
  const cg = v(gpu?.clock_gpu);
  const cm = v(gpu?.clock_mem);
  const powerText =
    power === null
      ? null
      : device.power_max_w
        ? t("value.pair", { used: power, total: t("unit.watt", { value: device.power_max_w }) })
        : t("unit.watt", { value: power });
  const small: [string, string | null][] = [
    [t("readout.temp"), temp === null ? null : t("unit.celsius", { value: temp })],
    [t("readout.fan"), fan === null ? null : t("unit.percent", { value: fan })],
    [t("readout.power"), powerText],
    [t("readout.clockGpu"), cg === null ? null : t("unit.mhz", { value: cg })],
    [t("readout.clockMem"), cm === null ? null : t("unit.mhz", { value: cm })],
  ];
  return (
    <>
      <div className="readout-main">
        <div className="readout-line">
          <span className="readout-label">{t("readout.util")}</span>
          <Value big text={util === null ? null : t("unit.percent", { value: util })} />
        </div>
        <Bar pct={util ?? 0} color="--curve-util" />
      </div>
      <div className="readout-main">
        <div className="readout-line">
          <span className="readout-label">{t("readout.mem")}</span>
          <Value big text={memText(used, total, t)} />
        </div>
        <Bar pct={used !== null && total ? (used / total) * 100 : 0} color="--curve-mem" />
      </div>
      <div className="readout-grid">
        {small.map(([label, text]) => (
          <div key={label} className="readout-cell">
            <span className="readout-label">{label}</span>
            <Value text={text} />
          </div>
        ))}
      </div>
    </>
  );
});

function GpuTitle({ device, collapsed, onToggle }: { device: Device; collapsed: boolean; onToggle: () => void }) {
  const t = useT();
  return (
    <button type="button" className="card-title" aria-expanded={!collapsed} onClick={onToggle}>
      {collapsed ? <ChevronRight /> : <ChevronDown />}
      <span className="gpu-badge">{t("monitor.gpu", { id: device.id })}</span>
      <span className="ellipsis" title={device.name}>
        {device.name}
      </span>
    </button>
  );
}

export interface GpuRowProps {
  device: Device;
  gpu: LiveGPU | undefined;
  collapsed: boolean;
  onToggle: (gpuID: number, collapsed: boolean) => void;
  from: number;
  to: number;
  data: ChartData | null;
  series: ChartSeries[];
  onWidth: (w: number) => void;
  onHover?: (t: number | null) => void;
}

// GpuRow 為一張 GPU 的卡片. 頂端的標題列左側為 GPU 名稱, 右側為曲線圖例; 其下左側為
// 文字讀數, 右側為曲線圖. 收合後只留標題一行. 監看畫面與歷史畫面共用.
export const GpuRow = memo(function GpuRow(p: GpuRowProps) {
  const t = useT();
  const toggle = () => p.onToggle(p.device.id, !p.collapsed);
  if (p.collapsed) {
    return (
      <section className="card card-collapsed">
        <GpuTitle device={p.device} collapsed onToggle={toggle} />
      </section>
    );
  }
  return (
    <section className="card gpu-card">
      <div className="gpu-head">
        <GpuTitle device={p.device} collapsed={false} onToggle={toggle} />
        <ChartLegend series={p.series} />
      </div>
      <div className="gpu-readouts">
        <Readouts device={p.device} gpu={p.gpu} />
      </div>
      <div className="gpu-chart">
        <Chart
          from={p.from}
          to={p.to}
          data={p.data}
          series={p.series}
          ariaLabel={t("monitor.gpuChart", { id: p.device.id })}
          gapTitle={(start, end) => t("gap.title", { start, end })}
          onWidth={p.onWidth}
          onHover={p.onHover}
        />
      </div>
    </section>
  );
});
