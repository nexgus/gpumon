import { MinusIcon, PlusIcon } from "../components/Icons";
import { formatSpan } from "../format";
import { useT } from "../i18n";
import { isMac } from "../platform";

// spanLevels 為縮放按鍵的固定級距 (微秒), 由短至長.
const spanLevels = [1, 5, 10, 30, 60, 180, 360, 720, 1440].map((m) => m * 60e6);

// stepSpan 回傳由 span 往較長 (dir 為 1) 或較短 (dir 為 -1) 方向的相鄰級距, 限制於 min 與 max
// 之間. span 不在級距上時 (以滾輪或捏合調整後), 取該方向最近的級距; 該方向已無級距時取邊界.
// 不足 1 秒的差距視為相等.
export function stepSpan(span: number, dir: 1 | -1, min: number, max: number): number {
  const eps = 1e6;
  const next = dir === 1 ? spanLevels.find((l) => l > span + eps) : [...spanLevels].reverse().find((l) => l < span - eps);
  return Math.min(max, Math.max(min, next ?? (dir === 1 ? max : min)));
}

// ZoomControls 為監看畫面工具列的縮放控制項, 由左至右為標籤, 縮小按鍵, 範圍長度與放大按鍵,
// 後三者位於同一個外框內. span, min 與 max 為顯示範圍的長度與其上下限 (微秒). 範圍長度只供
// 顯示, 欄寬固定, 文字長短變化時兩側按鍵不移動. 縮小按鍵把長度移至較長的相鄰級距, 放大按鍵
// 移至較短的相鄰級距, 以 onSpan 回報新的長度; 已達上下限的按鍵停用.
export function ZoomControls({ span, min, max, onSpan }: { span: number; min: number; max: number; onSpan: (span: number) => void }) {
  const t = useT();
  return (
    <div role="group" aria-label={t("zoom.group")} className="zoom-group">
      <span className="zoom-label">{t("zoom.span")}</span>
      <div className="zoom">
        <button
          type="button"
          className="zoom-btn"
          aria-label={t("zoom.out")}
          title={t(isMac ? "zoom.outTipMac" : "zoom.outTipWin")}
          disabled={span >= max}
          onClick={() => onSpan(stepSpan(span, 1, min, max))}
        >
          <MinusIcon />
        </button>
        <span className="zoom-span">{formatSpan(span, t)}</span>
        <button
          type="button"
          className="zoom-btn"
          aria-label={t("zoom.in")}
          title={t(isMac ? "zoom.inTipMac" : "zoom.inTipWin")}
          disabled={span <= min}
          onClick={() => onSpan(stepSpan(span, -1, min, max))}
        >
          <PlusIcon />
        </button>
      </div>
    </div>
  );
}
