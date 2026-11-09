// 介面文字的語言資源. 每種語言一個 JSON 檔案, 鍵相同; 所選語言缺少某個鍵時改用
// 英文. 新增語言時加入一個資源檔並登錄於 resources, 並於後端的 settings 套件加入
// 對應的語言值.
import { Fragment, createElement, type ReactNode } from "react";
import en from "./en.json";
import zhTW from "./zh-TW.json";
import type { Language } from "../api/types";
import { Store, useStore } from "../state/store";

export type MessageKey = keyof typeof en;

const resources: Record<Language, Partial<Record<MessageKey, string>>> = {
  en,
  "zh-TW": zhTW,
};

export const languageStore = new Store<Language>("en");

export function setLanguage(lang: Language): void {
  languageStore.set(resources[lang] ? lang : "en");
  document.documentElement.lang = lang;
}

export type Params = Record<string, string | number>;

// translate 回傳 key 於 lang 的文字, 並以 params 代換 {名稱} 形式的參數.
export function translate(lang: Language, key: MessageKey, params?: Params): string {
  const text = resources[lang]?.[key] ?? en[key] ?? key;
  if (!params) {
    return text;
  }
  return text.replace(/\{(\w+)\}/g, (m, name: string) => (name in params ? String(params[name]) : m));
}

// t 以目前的語言取得文字, 供元件以外的程式使用.
export function t(key: MessageKey, params?: Params): string {
  return translate(languageStore.get(), key, params);
}

export interface Translator {
  (key: MessageKey, params?: Params): string;
  // rich 與 t 相同, 但參數可為 React 節點, 例如以等寬字型標示的主機名稱.
  rich(key: MessageKey, params: Record<string, ReactNode>): ReactNode;
  lang: Language;
}

function makeTranslator(lang: Language): Translator {
  const fn = ((key: MessageKey, params?: Params) => translate(lang, key, params)) as Translator;
  fn.lang = lang;
  fn.rich = (key, params) => {
    const text = translate(lang, key);
    const parts = text.split(/(\{\w+\})/);
    return createElement(
      Fragment,
      null,
      ...parts.map((p, i) => {
        const m = /^\{(\w+)\}$/.exec(p);
        return createElement(Fragment, { key: i }, m && m[1] in params ? params[m[1]] : p);
      }),
    );
  };
  return fn;
}

const translators = new Map<Language, Translator>();

function translatorFor(lang: Language): Translator {
  let tr = translators.get(lang);
  if (!tr) {
    tr = makeTranslator(lang);
    translators.set(lang, tr);
  }
  return tr;
}

// useT 回傳目前語言的翻譯函數; 語言變更時使用它的元件重新繪製.
export function useT(): Translator {
  const lang = useStore(languageStore, (l) => l);
  return translatorFor(lang);
}
