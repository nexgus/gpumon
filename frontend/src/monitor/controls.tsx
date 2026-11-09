import { useId } from "react";
import type { TimeRange } from "../api/types";
import { useT } from "../i18n";

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
