import { MinusIcon, PlusIcon } from "../components/Icons";
import { useT } from "../i18n";
import { isMac } from "../platform";

// ZoomControls 為工具列的縮放控制項, 由左至右為縮小按鍵與放大按鍵.
// span, min 與 max 為顯示範圍的長度與其上下限 (微秒). 縮小使長度變為兩倍, 放大使長度
// 變為一半, 以 onZoom 回報長度的倍數, 結果由呼叫端限制於上下限之間; 已達上下限的按鍵
// 停用. outDisabled 為真時縮小按鍵另行停用, 例如歷史畫面的顯示全部狀態.
export function ZoomControls({
  span,
  min,
  max,
  onZoom,
  outDisabled = false,
}: {
  span: number;
  min: number;
  max: number;
  onZoom: (factor: number) => void;
  outDisabled?: boolean;
}) {
  const t = useT();
  return (
    <div role="group" aria-label={t("zoom.group")} className="zoom">
      <button
        type="button"
        className="zoom-btn"
        aria-label={t("zoom.out")}
        title={t(isMac ? "zoom.outTipMac" : "zoom.outTipWin")}
        disabled={outDisabled || span >= max}
        onClick={() => onZoom(2)}
      >
        <MinusIcon />
      </button>
      <button
        type="button"
        className="zoom-btn"
        aria-label={t("zoom.in")}
        title={t(isMac ? "zoom.inTipMac" : "zoom.inTipWin")}
        disabled={span <= min}
        onClick={() => onZoom(0.5)}
      >
        <PlusIcon />
      </button>
    </div>
  );
}
