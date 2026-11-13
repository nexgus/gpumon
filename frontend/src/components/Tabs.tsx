import { useLayoutEffect, useRef, type ReactNode, type WheelEvent } from "react";
import { CloseIcon } from "./Icons";

// TabList 為資料夾式分頁列, 監看畫面與關於視窗共用. 分頁超出寬度時左右捲動, 不換行; 捲軸不
// 顯示, 滾輪的上下捲動改為左右捲動分頁列. selectedKey 改變時把選取中的分頁捲入可視範圍.
export function TabList({
  label,
  selectedKey,
  className,
  children,
}: {
  label?: string;
  selectedKey: string | null;
  className?: string;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = ref.current?.querySelector<HTMLElement>(".tab.selected");
    const list = ref.current;
    if (!el || !list) {
      return;
    }
    const r = el.getBoundingClientRect();
    const lr = list.getBoundingClientRect();
    if (r.left < lr.left) {
      list.scrollLeft -= lr.left - r.left;
    } else if (r.right > lr.right) {
      list.scrollLeft += r.right - lr.right;
    }
  }, [selectedKey]);
  const onWheel = (e: WheelEvent<HTMLDivElement>) => {
    if (ref.current && e.deltaX === 0 && e.deltaY !== 0) {
      ref.current.scrollLeft += e.deltaY;
    }
  };
  return (
    <div ref={ref} role="tablist" aria-label={label} className={`tabs${className ? ` ${className}` : ""}`} onWheel={onWheel}>
      {children}
    </div>
  );
}

// Tab 為分頁列中的一個分頁. onClose 存在時於名稱右側顯示 "✕" 關閉鍵, closeLabel 為其滑鼠
// 停留提示與無障礙名稱.
export function Tab({
  selected,
  onSelect,
  onClose,
  closeLabel,
  children,
}: {
  selected: boolean;
  onSelect: () => void;
  onClose?: () => void;
  closeLabel?: string;
  children: ReactNode;
}) {
  return (
    <div role="presentation" className={`tab${selected ? " selected" : ""}${onClose ? " closable" : ""}`}>
      <button type="button" role="tab" aria-selected={selected} className="tab-label" onClick={onSelect}>
        {children}
      </button>
      {onClose && (
        <button type="button" className="tab-close" aria-label={closeLabel} title={closeLabel} onClick={onClose}>
          <CloseIcon />
        </button>
      )}
    </div>
  );
}
