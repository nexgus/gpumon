import { useEffect, useRef, useState } from "react";
import { backend } from "../api/backend";
import type { RecordedProcesses } from "../api/types";

const minInterval = 150;

// useProcessesAt 回傳記錄檔中時刻 t 之前最近一筆程序記錄; 沒有時為 null, 尚未查得時
// 為 undefined. t 隨滑鼠移動頻繁變化, 查詢以節流處理: 同一時間只有一個查詢進行中,
// 兩次查詢至少相隔 150 ms, 期間的變化只以最新的 t 再查一次.
export function useProcessesAt(path: string, t: number): RecordedProcesses | null | undefined {
  const [value, setValue] = useState<RecordedProcesses | null | undefined>(undefined);
  const target = useRef({ path, t });
  target.current = { path, t };
  const st = useRef({ busy: false, dirty: false, last: 0, timer: 0, alive: true });

  useEffect(() => {
    const s = st.current;
    s.alive = true;
    return () => {
      s.alive = false;
      window.clearTimeout(s.timer);
      s.timer = 0;
    };
  }, []);

  useEffect(() => {
    setValue(undefined);
  }, [path]);

  useEffect(() => {
    const s = st.current;
    const run = async () => {
      s.busy = true;
      s.dirty = false;
      s.last = Date.now();
      const q = target.current;
      try {
        const r = await backend.processesAt(q.path, q.t);
        if (s.alive && q.path === target.current.path) {
          setValue(r);
        }
      } catch {
        // 讀取失敗時保留前一次的內容.
      } finally {
        s.busy = false;
        if (s.dirty && s.alive) {
          schedule();
        }
      }
    };
    const schedule = () => {
      if (s.busy) {
        s.dirty = true;
        return;
      }
      if (s.timer) {
        return;
      }
      const wait = Math.max(0, minInterval - (Date.now() - s.last));
      s.timer = window.setTimeout(() => {
        s.timer = 0;
        void run();
      }, wait);
    };
    schedule();
  }, [path, t]);

  return value;
}
