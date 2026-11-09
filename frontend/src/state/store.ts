import { useSyncExternalStore } from "react";

// Store 為最小的外部狀態容器. 值以不可變的方式替換, 訂閱者經 useStore 以選取函數
// 取出所需的部分; 選取結果未變時元件不重新繪製, 使新取樣只更新相關的元件.
export class Store<T> {
  private value: T;
  private listeners = new Set<() => void>();

  constructor(initial: T) {
    this.value = initial;
  }

  get(): T {
    return this.value;
  }

  set(next: T): void {
    if (Object.is(next, this.value)) {
      return;
    }
    this.value = next;
    for (const l of [...this.listeners]) {
      l();
    }
  }

  update(fn: (v: T) => T): void {
    this.set(fn(this.value));
  }

  subscribe = (l: () => void): (() => void) => {
    this.listeners.add(l);
    return () => this.listeners.delete(l);
  };
}

// useStore 訂閱 store 並回傳 select 的結果. select 須回傳既有的參照或純量, 不可每次
// 建立新物件, 否則每次變更皆會重新繪製.
export function useStore<T, S>(store: Store<T>, select: (v: T) => S): S {
  return useSyncExternalStore(store.subscribe, () => select(store.get()));
}
