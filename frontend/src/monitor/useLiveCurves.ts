import { useEffect, useRef, useState } from "react";
import { backend } from "../api/backend";
import type { Field, QueryResult } from "../api/types";
import { fromResult, gridFor, gridRange, merge, tailRange, tailWidth, toResult, type LiveState } from "./liveCurves";

interface Params {
  id: string;
  connStart: number;
  to: number;
  span: number;
  width: number;
  fields: Field[];
}

// viewKey 為決定像素範圍, 欄位與連線起點的參數; 只有 to 改變時以增量更新. 連線起點改變
// 表示自動重新連線後另一次連線開始, 增量狀態內的舊曲線須丟棄.
function viewKey(p: Params): string {
  return `${p.id}|${p.connStart}|${p.span}|${p.width}|${p.fields.join(",")}`;
}

// useLiveCurves 查詢監看畫面主機於顯示範圍 [to - span, to] 的曲線資料. 像素範圍對齊絕對
// 時刻 (見 liveCurves.ts), 查詢範圍為顯示範圍所涉及的完整像素範圍. 顯示範圍長度, 曲線圖
// 寬度或欄位改變時重新查詢整段; 只有右緣 to 前進時, 降採樣的結果只查詢新取樣並併入,
// 每筆取樣的更新成本與顯示範圍長度無關; 結果為原始取樣點時, 取樣筆數不多於曲線圖寬度,
// 直接重新查詢. 同一時間只有一個查詢進行中, 進行中又有新參數時, 於完成後以最新的參數
// 再查一次. connStart 為本次連線第一筆取樣的時刻, 改變時重新查詢整段. enabled 為假或
// width 為 0 時不查詢.
export function useLiveCurves(
  id: string,
  enabled: boolean,
  connStart: number,
  to: number,
  span: number,
  width: number,
  fields: Field[],
): QueryResult | null {
  const [result, setResult] = useState<QueryResult | null>(null);
  const busy = useRef(false);
  const wanted = useRef<Params | null>(null);
  const alive = useRef(true);
  // done 為最近一次完成的查詢所對應的參數, state 為其可併入的狀態 (原始取樣點時為 null).
  const done = useRef<{ key: string; to: number } | null>(null);
  const state = useRef<LiveState | null>(null);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  useEffect(() => {
    if (!enabled) {
      setResult(null);
      done.current = null;
      state.current = null;
    }
  }, [enabled, id]);

  const fieldsKey = fields.join(",");
  useEffect(() => {
    if (!enabled || width <= 0 || to <= 0 || span <= 0 || fields.length === 0) {
      return;
    }
    wanted.current = { id, connStart, to, span, width, fields };

    const full = async (p: Params, key: string) => {
      const grid = gridFor(p.to, p.span, p.width);
      const r = gridRange(grid);
      const res = await backend.query(p.id, r.from, r.to, r.width, p.fields, true);
      if (!res) {
        return;
      }
      state.current = fromResult(res, grid, p.fields);
      done.current = { key, to: p.to };
      if (alive.current) {
        setResult(res);
      }
    };

    const step = async (p: Params) => {
      const key = viewKey(p);
      const st = state.current;
      if (st && done.current?.key === key && p.to >= done.current.to) {
        const next = gridFor(p.to, p.span, p.width);
        const r = tailRange(st, next);
        const tail = r ? await backend.query(p.id, r.from, r.to, tailWidth, p.fields, false) : null;
        if ((!r || tail) && state.current === st && merge(st, tail, next)) {
          done.current = { key, to: p.to };
          if (alive.current) {
            setResult(toResult(st));
          }
          return;
        }
        state.current = null;
      }
      await full(p, key);
    };

    const run = () => {
      const p = wanted.current;
      if (!p || busy.current) {
        return;
      }
      if (done.current && done.current.key === viewKey(p) && done.current.to === p.to) {
        return;
      }
      busy.current = true;
      step(p)
        .catch(() => {
          state.current = null;
          done.current = null;
        })
        .finally(() => {
          busy.current = false;
          if (alive.current && wanted.current !== p) {
            run();
          }
        });
    };
    run();
    // fields 以 fieldsKey 代表其內容, 不列入相依.
  }, [id, enabled, connStart, to, span, width, fieldsKey]);

  return result;
}
