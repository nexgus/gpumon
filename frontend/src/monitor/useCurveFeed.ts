import { useEffect, useRef, useState } from "react";
import { backend } from "../api/backend";
import type { Field, QueryResult } from "../api/types";

interface Params {
  id: string;
  from: number;
  to: number;
  width: number;
  fields: Field[];
}

function key(p: Params): string {
  return `${p.id}|${p.from}|${p.to}|${p.width}|${p.fields.join(",")}`;
}

// useCurveFeed 查詢主機於 [from, to] 的曲線資料, 參數變更時重新查詢. 同一時間只有
// 一個查詢進行中; 進行中又有新參數時, 於完成後只以最新的參數再查一次, 使取樣間隔
// 很短時查詢不會堆積. enabled 為假或 width 為 0 時不查詢. fetcher 省略時查詢監看
// 畫面的主機; 歷史畫面傳入查詢記錄檔的函數, 此時 id 為記錄檔的路徑.
export function useCurveFeed(
  id: string,
  enabled: boolean,
  from: number,
  to: number,
  width: number,
  fields: Field[],
  fetcher?: (id: string, from: number, to: number, width: number, fields: Field[]) => Promise<QueryResult | null>,
): QueryResult | null {
  const [result, setResult] = useState<QueryResult | null>(null);
  const busy = useRef(false);
  const pending = useRef<Params | null>(null);
  const alive = useRef(true);
  const last = useRef("");
  const fetchRef = useRef(fetcher);
  fetchRef.current = fetcher;

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  useEffect(() => {
    if (!enabled) {
      setResult(null);
      last.current = "";
    }
  }, [enabled, id]);

  const fieldsKey = fields.join(",");
  useEffect(() => {
    if (!enabled || width <= 0 || to <= from || fields.length === 0) {
      return;
    }
    const p: Params = { id, from, to, width, fields };
    if (key(p) === last.current) {
      return;
    }
    const run = (q: Params) => {
      busy.current = true;
      last.current = key(q);
      (fetchRef.current ?? ((...a) => backend.query(...a)))(q.id, q.from, q.to, q.width, q.fields)
        .then(
          (res) => {
            if (alive.current) {
              setResult(res);
            }
          },
          () => undefined,
        )
        .finally(() => {
          busy.current = false;
          const next = pending.current;
          pending.current = null;
          if (next && alive.current && key(next) !== last.current) {
            run(next);
          }
        });
    };
    if (busy.current) {
      pending.current = p;
    } else {
      run(p);
    }
    // fields 以 fieldsKey 代表其內容, 不列入相依.
  }, [id, enabled, from, to, width, fieldsKey]);

  return result;
}
