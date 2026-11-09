import { useEffect, useRef, useState } from "react";
import type { Curve } from "../api/types";
import { allCurves, curveColor } from "../chart/gpuSeries";
import { ChevronDown } from "../components/Icons";
import { useT } from "../i18n";
import { choicesStore, defaultCurves, setCurves, settingsStore } from "../state/app";
import { useStore } from "../state/store";

// CurvesMenu 為工具列右側的曲線選單, 勾選要畫的曲線. 此選擇為全域設定.
export function CurvesMenu() {
  const t = useT();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const curves = useStore(settingsStore, (s) => s?.curves) ?? defaultCurves;
  const maxCurves = useStore(choicesStore, (c) => c?.max_curves) ?? allCurves.length;

  useEffect(() => {
    if (!open) {
      return;
    }
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const toggle = (c: Curve, on: boolean) => {
    const next = allCurves.filter((x) => (x === c ? on : curves.includes(x)));
    void setCurves(next);
  };

  return (
    <div className="menu-anchor" ref={ref}>
      <button type="button" className="btn" aria-expanded={open} aria-haspopup="true" onClick={() => setOpen((o) => !o)}>
        {t("monitor.curves")} <ChevronDown size={12} />
      </button>
      {open && (
        <div className="menu curves-menu">
          {allCurves.map((c) => {
            const checked = curves.includes(c);
            const id = `curve-${c}`;
            return (
              <div key={c} className="curves-item">
                <input
                  id={id}
                  type="checkbox"
                  checked={checked}
                  disabled={!checked && curves.length >= maxCurves}
                  onChange={(e) => toggle(c, e.target.checked)}
                />
                <span className="curves-swatch" style={{ background: `var(${curveColor[c]})` }} />
                <label htmlFor={id}>{t(`curve.${c}`)}</label>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
