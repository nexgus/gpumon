// 第三方授權分頁的資料整理. 資料檔 third-party-licenses.json 由建置腳本產生, 每個元件
// 各附其授權檔的全文; 本檔案把全文拆成各元件的著作權聲明與授權本文, 同種而本文相同的
// 授權只列一次全文.
import data from "../third-party-licenses.json";

export interface LicenseEntry {
  name: string;
  version: string;
  url: string;
  license: string;
  text: string;
}

interface LicenseData {
  go: { direct: LicenseEntry[]; indirect: LicenseEntry[] };
  npm: { direct: LicenseEntry[]; indirect: LicenseEntry[] };
  other: LicenseEntry[];
}

// Notice 為一個元件的著作權聲明; text 為空字串表示授權檔未附聲明.
export interface Notice {
  name: string;
  text: string;
}

// LicenseText 為一份授權全文與適用該全文的元件. id 為頁內錨點, title 為授權種類;
// 同種授權的本文有多種寫法時 (例如 BSD-3-Clause 的第三款載明不同的機構), 分別列出
// 全文, title 的訂法見 titleVariants.
export interface LicenseText {
  id: string;
  title: string;
  body: string;
  notices: Notice[];
}

export interface Component {
  name: string;
  url: string;
  license: string;
  textID: string;
}

export interface LicenseView {
  direct: Component[];
  indirect: Component[];
  texts: LicenseText[];
}

const noticeMarker = "\n--- NOTICE ---\n";

// isCopyrightLine 判斷授權檔開頭的一行是否屬於著作權聲明.
function isCopyrightLine(line: string): boolean {
  return /^(copyright\b|\(c\)|©)/i.test(line) || /^all rights reserved\.?$/i.test(line);
}

// isTitleLine 判斷一行是否為授權名稱的標題, 例如 "MIT License" 或 "The MIT License (MIT)".
function isTitleLine(line: string): boolean {
  return line.length < 60 && /\blicen[cs]e\b/i.test(line) && !/licensed|permission|copyright/i.test(line);
}

// splitLicense 把授權檔全文拆為著作權聲明與授權本文. 著作權聲明為全文開頭 (標題之後)
// 連續的著作權行, 以及授權檔所附的 NOTICE; 其餘為授權本文, 標題保留於本文. 本文中的
// 範本文字 (例如 Apache License 附錄的 "Copyright [yyyy]") 不視為聲明. terms 為本文
// 去除標題後的部分, 供比較兩份授權是否相同.
export function splitLicense(text: string): { notice: string; body: string; terms: string } {
  let main = text;
  let extra = "";
  const at = text.indexOf(noticeMarker);
  if (at >= 0) {
    main = text.slice(0, at);
    extra = text.slice(at + noticeMarker.length).trim();
  }
  const lines = main.split("\n");
  const title: string[] = [];
  const notice: string[] = [];
  let i = 0;
  for (; i < lines.length; i++) {
    const line = lines[i].trim();
    if (line === "") {
      continue;
    }
    if (notice.length === 0 && isTitleLine(line)) {
      title.push(lines[i]);
      continue;
    }
    if (isCopyrightLine(line)) {
      notice.push(line);
      continue;
    }
    break;
  }
  const trim = (s: string) => s.replace(/^\n+|\s+$/g, "");
  const terms = trim(lines.slice(i).join("\n"));
  const body = notice.length === 0 ? trim(main) : trim([...title, ...(title.length > 0 ? [""] : []), terms].join("\n"));
  return { notice: [...notice, ...(extra ? [extra] : [])].join("\n"), body, terms };
}

// bodyKey 為比較兩份授權本文是否相同的鍵, 忽略換行位置, 引號的寫法與分隔線的長度.
function bodyKey(body: string): string {
  return body
    .replace(/[“”]/g, '"')
    .replace(/[‘’]/g, "'")
    .replace(/-{3,}/g, "---")
    .replace(/\s+/g, " ")
    .trim();
}

const namedOrg = /\bneither the name of (?:the )?(.+?)\s+nor\b/i;

// orgOf 取出授權本文中 "Neither the name of <機構> nor" 所載的機構名稱, 例如 BSD-3-Clause
// 第三款的 "Google LLC"; 本文沒有這句話時回傳空字串.
function orgOf(body: string): string {
  const m = namedOrg.exec(body.replace(/\s+/g, " "));
  return m ? m[1].trim() : "";
}

// titleVariants 為同種授權而本文不同的各份全文訂定標題. 各份皆載有機構名稱且彼此
// 不同時, 每份的標題為授權種類加上該機構名稱, 例如 "BSD-3-Clause (Google LLC)";
// 否則第二份起於授權種類後加上序號, 例如 "BSD-3-Clause (2)".
function titleVariants(license: string, group: LicenseText[]): void {
  const orgs = group.map((lt) => orgOf(lt.body));
  if (orgs.every((o) => o !== "") && new Set(orgs).size === orgs.length) {
    group.forEach((lt, i) => (lt.title = `${license} (${orgs[i]})`));
    return;
  }
  group.forEach((lt, i) => (lt.title = i === 0 ? license : `${license} (${i + 1})`));
}

// byName 依元件名稱排序, 不分大小寫, 名稱只有大小寫不同時再依原字串. 以字元碼比較,
// 不用 localeCompare, 以免語系的排序規則忽略 "@" 等符號.
function byName(a: LicenseEntry, b: LicenseEntry): number {
  const la = a.name.toLowerCase();
  const lb = b.name.toLowerCase();
  if (la !== lb) {
    return la < lb ? -1 : 1;
  }
  return a.name < b.name ? -1 : a.name > b.name ? 1 : 0;
}

// buildLicenseView 整理授權清單. 直接引用與間接引用兩張表各自把 Go, npm 與其他素材合在
// 一起, 依元件名稱排序 (見 byName). 授權全文依第一個引用它的元件的順序排列, 直接引用在前.
export function buildLicenseView(d: LicenseData): LicenseView {
  const texts: LicenseText[] = [];
  const byKey = new Map<string, LicenseText>();
  const byLicense = new Map<string, LicenseText[]>();
  const component = (e: LicenseEntry): { entry: LicenseEntry; text: LicenseText } => {
    const { notice, body, terms } = splitLicense(e.text);
    const key = `${e.license}\n${bodyKey(terms)}`;
    let lt = byKey.get(key);
    if (!lt) {
      lt = { id: `license-${texts.length + 1}`, title: e.license, body, notices: [] };
      byKey.set(key, lt);
      texts.push(lt);
      byLicense.set(e.license, [...(byLicense.get(e.license) ?? []), lt]);
    }
    lt.notices.push({ name: e.name, text: notice });
    return { entry: e, text: lt };
  };
  const direct = [...d.go.direct, ...d.npm.direct, ...d.other].sort(byName).map(component);
  const indirect = [...d.go.indirect, ...d.npm.indirect].sort(byName).map(component);
  for (const [license, group] of byLicense) {
    if (group.length > 1) {
      titleVariants(license, group);
    }
  }
  const toComponent = ({ entry, text }: { entry: LicenseEntry; text: LicenseText }): Component => ({
    name: entry.name,
    url: entry.url,
    license: text.title,
    textID: text.id,
  });
  return { direct: direct.map(toComponent), indirect: indirect.map(toComponent), texts };
}

const listMarker = /^(\*|-|•|\(?[a-z0-9]{1,3}[.)])\s/i;
const ruleLine = /^[-=_*]{3,}$/;
const headingLine = /^[A-Z][A-Z0-9 &/-]{0,39}$/;

// paragraphs 把授權本文重新排版為段落以便閱讀: 以空行分段, 段內的換行改為空白, 但
// 清單項目, 分隔線與段首的大寫小標題 (例如 OFL 的 "PREAMBLE") 各自起一行. 每個段落
// 為其各行的文字. 文字本身不變, 只調整空白.
export function paragraphs(body: string): string[][] {
  const out: string[][] = [];
  for (const block of body.split(/\n\s*\n/)) {
    const lines: string[] = [];
    let prevRule = false;
    for (const raw of block.split("\n")) {
      const line = raw.trim();
      if (line === "") {
        continue;
      }
      const rule = ruleLine.test(line) || (lines.length === 0 && headingLine.test(line));
      if (lines.length === 0 || rule || prevRule || listMarker.test(line)) {
        lines.push(line);
      } else {
        lines[lines.length - 1] += ` ${line}`;
      }
      prevRule = rule;
    }
    if (lines.length > 0) {
      out.push(lines);
    }
  }
  return out;
}

let cached: LicenseView | null = null;

// licenseView 回傳由 third-party-licenses.json 整理出的授權清單.
export function licenseView(): LicenseView {
  if (!cached) {
    cached = buildLicenseView(data as LicenseData);
  }
  return cached;
}
