import { useEffect, useState } from "react";
import { backend } from "../api/backend";
import type { RecordingMaxima } from "../api/types";

// useMaxima 回傳記錄檔中每張 GPU 每個欄位於整個記錄檔的最大值. 只於開啟記錄檔時查詢一次,
// 連線中的記錄檔之後亦不更新. 尚未查得時為 undefined, 查詢失敗時為 null.
export function useMaxima(path: string): RecordingMaxima | null | undefined {
  const [value, setValue] = useState<RecordingMaxima | null | undefined>(undefined);
  useEffect(() => {
    let alive = true;
    setValue(undefined);
    backend.maxima(path).then(
      (m) => alive && setValue(m),
      () => alive && setValue(null),
    );
    return () => {
      alive = false;
    };
  }, [path]);
  return value;
}
