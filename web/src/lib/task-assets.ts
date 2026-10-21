import type { NewAssetType } from "@/lib/types";

const ASSET_TYPE_LABELS: Record<NewAssetType, string> = {
  app: "アプリ",
  endpoint: "インターフェース",
  ip: "IP",
  root_domain: "ルートドメイン",
  service: "サービス",
  subdomain: "サブドメイン",
};

const TASK_ASSET_SOURCE_LABELS: Record<string, string> = {
  agent: "Agent 検出",
  anchor: "ブラックボードアンカー",
  api: "アセット API",
  company: "企業関連付け",
  legacy: "履歴関連付け",
  manual: "手動追加",
  system: "システム関連付け",
  task: "タスク初期化",
};

export function taskAssetTypeLabel(type: NewAssetType): string {
  return ASSET_TYPE_LABELS[type];
}

export function taskAssetSourceLabel(source: string): string {
  return TASK_ASSET_SOURCE_LABELS[source] ?? source;
}
