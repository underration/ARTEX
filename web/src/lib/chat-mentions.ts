export const mentionKinds = [
  { kind: "finding", label: "脆弱性", alias: "finding" },
  { kind: "asset", label: "アセット", alias: "asset" },
  { kind: "company", label: "企業", alias: "company" },
  { kind: "endpoint", label: "インターフェース", alias: "api" },
  { kind: "ip", label: "IP", alias: "ip" },
  { kind: "app", label: "アプリ", alias: "app" },
  { kind: "root_domain", label: "ドメイン", alias: "domain" },
  { kind: "subdomain", label: "サブドメイン", alias: "subdomain" },
  { kind: "service", label: "サービス", alias: "service" },
] as const;

export type MentionKind = (typeof mentionKinds)[number]["kind"];
export interface ChatMention {
  kind: MentionKind;
  id: number;
  label: string;
  description: string;
}

export function activeMention(value: string, caret: number) {
  const before = value.slice(0, caret);
  const start = before.lastIndexOf("@");
  if (start < 0 || (start > 0 && /[\w.+/-]/.test(before[start - 1]))) return null;
  const query = before.slice(start + 1);
  if (/[[\]\r\n@]/.test(query) || query.length > 220) return null;
  return { start, end: caret, query };
}

const legacyAliases: Record<string, string[]> = {
  finding: ["漏洞"],
  asset: ["资产"],
  company: ["企业"],
  endpoint: ["接口"],
  ip: [],
  app: ["应用"],
  root_domain: ["域名"],
  subdomain: ["子域名"],
  service: ["服务"],
};

export function mentionSearch(query: string) {
  const text = query.trimStart().toLowerCase();
  for (const item of mentionKinds) {
    const aliases = [item.label.toLowerCase(), item.alias, ...(legacyAliases[item.kind] ?? [])];
    for (const alias of aliases) {
      if (text === alias || text.startsWith(`${alias} `) || (/[^a-z]/.test(alias) && text.startsWith(alias))) {
        return { kind: item.kind, query: query.trimStart().slice(alias.length).trim(), categories: [] };
      }
    }
  }
  const categories = mentionKinds.filter(
    (item) =>
      item.label.toLowerCase().startsWith(text) ||
      item.alias.startsWith(text) ||
      (legacyAliases[item.kind] ?? []).some((a) => a.startsWith(text)),
  );
  return { kind: "" as const, query: query.trim(), categories };
}

export function mentionToken(item: ChatMention) {
  const kind = mentionKinds.find((entry) => entry.kind === item.kind)?.label ?? "アセット";
  const label = item.label
    .replace(/[[\]]/g, (char) => (char === "[" ? "（" : "）"))
    .replace(/\s+/g, " ")
    .slice(0, 100);
  return `@[${kind}#${item.id} ${label}]`;
}

export function selectedMentions(value: string) {
  return [...value.matchAll(/@\[(脆弱性|アセット|企業|インターフェース|アプリ|ドメイン|サブドメイン|サービス|漏洞|资产|企业|接口|IP|应用|域名|子域名|服务)#([0-9]+)(?: ([^\]\r\n]*))?\]/g)].map(
    (match) => ({
      token: match[0],
      label: `${match[1]} #${match[2]}${match[3] ? ` · ${match[3]}` : ""}`,
      start: match.index,
    }),
  );
}
