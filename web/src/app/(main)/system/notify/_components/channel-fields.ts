// 渠道字段表与配置值的解析工具。
//
// 与页面拆开是因为这一份是**数据**而不是视图：它描述每种渠道有哪些字段、
// 各自该用什么控件，以及表单文本到配置值（JSON）的双向转换。
// 单独放一个文件后，新增渠道只需要动这里，页面本身不必改。
// 渠道类型的展示名与简介。放在前端是因为它只影响文案，后端不需要知道。
export const KIND_LABEL: Record<string, string> = {
  dingtalk: "DingTalk",
  feishu: "Feishu (Lark)",
  wecom: "WeCom (企業微信)",
  webhook: "汎用 Webhook",
  telegram: "Telegram",
  email: "メール",
};

// 各渠道的配置字段定义。
//
// 这里刻意保留一份前端字段表，而不是让后端下发 schema：后端只负责
// Validate（必填/格式），UI 需要的是布局与控件类型，两者关注的不是同一件事。
// 唯一的耦合点是 secret_keys —— 哪些字段该渲染成密码框由后端给出，
// 因为只有渠道实现自己清楚哪些值算凭据（企业微信的整个 Webhook 就是凭据，
// 而钉钉的只是其中一个 secret）。新增渠道时这里少一个条目只会让表单变空白，
// 不会静默出错（下面的 hasFields 会提示）。
export type FieldKind = "text" | "password" | "number" | "select" | "textarea" | "switch" | "kv" | "list";
export interface FieldDef {
  key: string;
  label: string;
  kind: FieldKind;
  placeholder?: string;
  help?: string;
  options?: { value: string; label: string }[];
}
export const CHANNEL_FIELDS: Record<string, FieldDef[]> = {
  dingtalk: [
    {
      key: "webhook",
      label: "Webhook URL",
      kind: "text",
      placeholder: "https://oapi.dingtalk.com/robot/send?access_token=...",
    },
    {
      key: "secret",
      label: "署名シークレット",
      kind: "password",
      help: "Bot のセキュリティ設定で「署名」を選択した場合に入力します。「カスタムキーワード」またはセキュリティ設定未有効の場合は空欄にしてください",
    },
  ],
  feishu: [
    {
      key: "webhook",
      label: "Webhook URL",
      kind: "text",
      placeholder: "https://open.feishu.cn/open-apis/bot/v2/hook/...",
    },
    { key: "secret", label: "署名検証シークレット", kind: "password", help: "Bot で「署名検証」を有効にしている場合に入力し、それ以外は空欄にしてください" },
  ],
  wecom: [
    {
      key: "webhook",
      label: "Webhook URL",
      kind: "text",
      placeholder: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...",
    },
  ],
  webhook: [
    { key: "url", label: "ターゲット URL", kind: "text", placeholder: "https://your-endpoint.example.com/hook" },
    {
      key: "method",
      label: "リクエストメソッド",
      kind: "select",
      options: [
        { value: "POST", label: "POST（ボディあり）" },
        { value: "PUT", label: "PUT（ボディあり）" },
        { value: "PATCH", label: "PATCH（ボディあり）" },
        { value: "GET", label: "GET（ボディなし）" },
      ],
    },
    { key: "headers", label: "カスタムリクエストヘッダー", kind: "kv", help: "1行につき KEY=VALUE（例：Authorization=Bearer xxx）" },
    {
      key: "body_template",
      label: "リクエストボディテンプレート",
      kind: "textarea",
      help:
        "空欄の場合は組み込みのデフォルトテンプレートを使用します。変数：{{.Title}} {{.Batch}} {{.Count}} {{.HomeURL}} {{.SentAt}}、" +
        "および range .Items 内の .Name/.VulnClass/.Severity/.Summary/.Assets/.DetailURL/.StatusLabel。" +
        "タイトル内の引用符で JSON が壊れるのを防ぐため、文字列の挿入には {{.Xxx}} ではなく {{json .Xxx}} を使用してください。",
    },
  ],
  telegram: [
    { key: "bot_token", label: "Bot Token", kind: "password", placeholder: "123456:ABC-DEF..." },
    { key: "chat_id", label: "Chat ID", kind: "text", placeholder: "-1001234567890" },
    {
      key: "base_url",
      label: "API URL",
      kind: "text",
      placeholder: "https://api.telegram.org",
      help: "空欄の場合は公式 URL を使用します。セルフホストの Bot API リバースプロキシを使用する場合に入力してください",
    },
  ],
  email: [
    { key: "host", label: "SMTP サーバー", kind: "text", placeholder: "smtp.example.com" },
    {
      key: "port",
      label: "ポート",
      kind: "number",
      placeholder: "587",
      help: "587 は STARTTLS を使用します。465 の場合は「暗黙的 TLS」をオンにしてください",
    },
    { key: "username", label: "アカウント", kind: "text" },
    { key: "password", label: "パスワード / アプリパスワード", kind: "password" },
    { key: "from", label: "送信元", kind: "text", placeholder: "artex@example.com" },
    { key: "to", label: "宛先", kind: "list", help: "複数のアドレスはカンマで区切ってください" },
    { key: "tls", label: "暗黙的 TLS", kind: "switch", help: "465 ポートの場合はオンにしてください。587 の場合はオフのままにします（自動的に STARTTLS になります）" },
  ],
};

export const SEVERITY_OPTIONS = [
  { value: "", label: "制限なし" },
  { value: "low", label: "低リスク以上" },
  { value: "medium", label: "中リスク以上" },
  { value: "high", label: "高リスク以上" },
  { value: "critical", label: "緊急のみ" },
];

export type ChannelForm = {
  name: string;
  kind: string;
  mode: "realtime" | "digest";
  enabled: boolean;
  ratePerMin: string;
  config: Record<string, unknown>;
  minSeverity: string;
  includeText: string;
  excludeText: string;
  taskIDsText: string;
  assetIDsText: string;
  onStatusChange: boolean;
};

export const emptyForm = (kind: string): ChannelForm => ({
  name: "",
  kind,
  mode: "realtime",
  enabled: true,
  ratePerMin: "",
  config: {},
  minSeverity: "",
  includeText: "",
  excludeText: "",
  taskIDsText: "",
  assetIDsText: "",
  onStatusChange: false,
});

// parseKV 解析「每行 KEY=VALUE」的文本域。
export function parseKV(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const t = line.trim();
    if (!t) continue;
    const i = t.indexOf("=");
    if (i > 0) out[t.slice(0, i).trim()] = t.slice(i + 1).trim();
  }
  return out;
}
// parseIDs 解析逗号/空白分隔的 id 列表。
export function parseIDs(text: string): number[] {
  return text
    .split(/[\s,，]+/)
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s) => Number(s))
    .filter((n) => Number.isFinite(n) && n > 0);
}
// parseKeywords 解析行/逗号分隔的关键词列表（漏洞类型名可能含空格，所以按行或逗号切）。
export function parseKeywords(text: string): string[] {
  return text
    .split(/[\n,，]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}
