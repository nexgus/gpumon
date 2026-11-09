import { useEffect, useId, useRef, useState } from "react";
import type { TimeRange } from "../api/types";
import { useT } from "../i18n";
import { choicesStore, setHostInterval } from "../state/app";
import { useStore } from "../state/store";
import { formatInterval } from "./hostText";

// RangeSelect 為顯示範圍選單.
export function RangeSelect({
  value,
  ranges,
  onChange,
}: {
  value: TimeRange;
  ranges: TimeRange[];
  onChange: (r: TimeRange) => void;
}) {
  const t = useT();
  const id = useId();
  return (
    <div className="ctl">
      <label htmlFor={id} className="ctl-label">
        {t("monitor.timeRange")}
      </label>
      <select id={id} className="select" value={value} onChange={(e) => onChange(e.target.value as TimeRange)}>
        {ranges.map((r) => (
          <option key={r} value={r}>
            {t(`range.${r}`)}
          </option>
        ))}
      </select>
    </div>
  );
}

// IntervalSlider 為取樣間隔滑桿. 拖曳中只更新顯示的數值, 停止移動 150 ms 或放開時
// 送出新的取樣間隔.
export function IntervalSlider({ hostID, value }: { hostID: string; value: number }) {
  const t = useT();
  const id = useId();
  const choices = useStore(choicesStore, (c) => c);
  const min = choices?.min_interval_ms ?? 200;
  const max = choices?.max_interval_ms ?? 10000;
  const [local, setLocal] = useState(value);
  const timer = useRef<number | undefined>(undefined);
  const editing = useRef(false);

  useEffect(() => {
    if (!editing.current) {
      setLocal(value);
    }
  }, [value]);

  useEffect(() => () => window.clearTimeout(timer.current), []);

  const commit = (ms: number) => {
    window.clearTimeout(timer.current);
    editing.current = false;
    if (ms !== value) {
      void setHostInterval(hostID, ms);
    }
  };

  return (
    <div className="ctl">
      <label htmlFor={id} className="ctl-label">
        {t("monitor.interval")}
      </label>
      <input
        id={id}
        type="range"
        className="slider"
        min={min}
        max={max}
        step={100}
        value={local}
        onChange={(e) => {
          const ms = Number(e.target.value);
          editing.current = true;
          setLocal(ms);
          window.clearTimeout(timer.current);
          timer.current = window.setTimeout(() => commit(ms), 150);
        }}
        onPointerUp={() => commit(local)}
        onKeyUp={() => commit(local)}
      />
      <span className="ctl-value">{formatInterval(local, t)}</span>
    </div>
  );
}
