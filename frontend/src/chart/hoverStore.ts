import { Store } from "../state/store";

// SharedSelect 為選取中的時段. from 與 to 為選取結果的起訖時刻 (Unix 微秒, 已依顯示範圍的
// 下限擴展), src 為正在拖曳選取的曲線圖的識別字串.
export interface SharedSelect {
  from: number;
  to: number;
  src: string;
}

// SharedHover 為同一畫面的曲線圖共用的滑鼠狀態. t 為滑鼠所指的時刻 (Unix 微秒), 滑鼠
// 不在任一張曲線圖上時為 null; src 為滑鼠所在曲線圖的識別字串. panning 為任一張曲線圖
// 正在平移, 此時所有曲線圖隱藏游標與數值提示. select 為任一張曲線圖正在選取的時段,
// 所有曲線圖於該時段畫出選取區並隱藏游標與數值提示; 未選取時為 null.
export interface SharedHover {
  t: number | null;
  src: string | null;
  panning: boolean;
  select: SharedSelect | null;
}

// createHoverStore 建立一個畫面專用的共用滑鼠狀態, 由該畫面的曲線圖共用.
export function createHoverStore(): Store<SharedHover> {
  return new Store<SharedHover>({ t: null, src: null, panning: false, select: null });
}
