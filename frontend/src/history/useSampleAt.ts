import { useEffect, useState } from "react";
import { backend } from "../api/backend";
import type { RecordedSample } from "../api/types";

// SampleAtResult 為一次取樣查詢的結果; t 為查詢的時刻, sample 為 null 表示該時刻沒有
// 取樣或查詢失敗.
export interface SampleAtResult {
  path: string;
  t: number;
  sample: RecordedSample | null;
}

// useSampleAt 查詢記錄檔中時刻不晚於 t 的最近一筆取樣, t 為 null 時不查詢. 回傳最近一次
// 完成的查詢結果, 可能屬於先前的 t; 呼叫端以結果中的 t 判斷是否已查得目前的時刻.
// 較晚送出的查詢先完成時, 較早的結果不再採用.
export function useSampleAt(path: string, t: number | null): SampleAtResult | undefined {
  const [value, setValue] = useState<SampleAtResult | undefined>(undefined);
  useEffect(() => {
    if (t === null) {
      return;
    }
    let alive = true;
    const done = (sample: RecordedSample | null) => alive && setValue({ path, t, sample });
    backend.sampleAt(path, t).then(done, () => done(null));
    return () => {
      alive = false;
    };
  }, [path, t]);
  return value;
}
