import { useEffect, useRef, type ReactNode } from "react";

interface DialogProps {
  title: string;
  width?: number;
  children: ReactNode;
  // onCancel 於按下 Esc 時呼叫, 未提供時 Esc 不關閉視窗; onSubmit 為表單送出 (含
  // 按下 Enter) 時呼叫.
  onCancel?: () => void;
  onSubmit?: () => void;
}

// Dialog 為置中的對話框, 背後以遮罩覆蓋主視窗. 開啟時把焦點移至第一個可輸入的欄位,
// 沒有欄位時移至主要按鍵.
export function Dialog({ title, width = 380, children, onCancel, onSubmit }: DialogProps) {
  const ref = useRef<HTMLFormElement>(null);

  useEffect(() => {
    const el = ref.current;
    const target =
      el?.querySelector<HTMLElement>("input:not([disabled])") ?? el?.querySelector<HTMLElement>("button.btn-primary, button.btn-danger");
    target?.focus();
  }, []);

  return (
    <div className="overlay">
      <form
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="dialog"
        style={{ width }}
        onSubmit={(e) => {
          e.preventDefault();
          onSubmit?.();
        }}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            e.preventDefault();
            onCancel?.();
          }
        }}
      >
        <div className="dialog-title">{title}</div>
        {children}
      </form>
    </div>
  );
}

export function DialogButtons({ children }: { children: ReactNode }) {
  return <div className="dialog-buttons">{children}</div>;
}
