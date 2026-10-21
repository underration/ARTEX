"use client";

// LLM 重试配置的共用件：五层重试各自的「次数 + 间隔」。
//
// 五层从内到外：建连(SDK) → 空响应(SDK) → 同 provider 安全窗口 → 轮询熔断 → 意图重跑。
// 前三层跟着端点走，所以每个模型配置都能覆盖全局默认；后两层是进程级的，只有全局一份。
//
// 所有输入都遵循同一套「留空 = 不配置」语义，与后端 db.RetryRule 一致：
//   次数   空/0 = 用内置默认 | -1 = 关闭这层重试 | >0 = 用这个次数
//   间隔   空/0 = 用这层原本的指数退避 | >0 = 改用这个固定毫秒间隔

import * as React from "react";

import { Loader2Icon, SaveIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import type { LLMRetryOverride, LLMRetryPolicy, LLMRetryRule } from "@/lib/types";

export const ZERO_RULE: LLMRetryRule = { attempts: 0, interval_ms: 0 };
export const ZERO_OVERRIDE: LLMRetryOverride = {
  connect: ZERO_RULE,
  empty: ZERO_RULE,
  stream: ZERO_RULE,
};
const ZERO_POLICY: LLMRetryPolicy = {
  ...ZERO_OVERRIDE,
  breaker: ZERO_RULE,
  intent: ZERO_RULE,
};

type LayerMeta = {
  title: string;
  /** 这层重试发生在哪、由谁执行 */
  where: string;
  /** 什么样的错误会走到这层——具体到状态码，别让人猜 */
  trigger: string;
  /** 长得像但【不】走这层的错误，省得填了没反应还以为是 bug */
  skips?: string;
  desc: string;
  attemptsLabel: string;
  /** 次数留空时的默认值，用于占位符 */
  defAttempts: number;
  /** 间隔留空时的默认策略，用于占位符 */
  defInterval: string;
  /** 次数填 -1 的含义 */
  offHint: string;
};

export const RETRY_LAYERS = {
  connect: {
    title: "接続リトライ",
    where: "SDK · 200 応答を受信する前",
    trigger:
      "接続失敗または 200 未受信：接続リセット / 読み書きタイムアウト / DNS 失敗などのネットワーク層エラー、および HTTP 408、429、500、502、503、504。",
    skips: "その他のステータスコード（400 / 401 / 403 / 404 / 413 / 422 など）は確定的な拒否であり、再送しても同様に失敗するため即座に上位へスローされます。",
    desc: "同じリクエストをそのまま再送します。ストリームが一度開始（200 を受信）した後の途中切断はこの層では扱いません。",
    attemptsLabel: "リトライ回数",
    defAttempts: 3,
    defInterval: "0.5s→1s→2s 指数バックオフ（上限 8s）",
    offHint: "-1 = 一切リトライせず、失敗時に即座に上位へスロー",
  },
  empty: {
    title: "空レスポンスリトライ",
    where: "SDK · openai フォーマットのみ",
    trigger:
      "HTTP 200、finish_reason が正常な stop でありながら、レスポンス全体にコンテンツブロックが1つもない場合（ゲートウェイの空フレーム、思考フィールドのフレーム欠落、サンプリングの不調などで発生）。",
    skips: "max_tokens による切り詰めでコンテンツがない場合は対象外（出力上限の引き上げで解決すべきであり、再送しても同じ結果になります）。",
    desc: "プロンプト全体を再送するため、長いコンテキストでは高コストになります。回数は控えめに設定してください。",
    attemptsLabel: "リトライ回数",
    defAttempts: 2,
    defInterval: "0.5s→1s→2s 指数バックオフ（上限 8s）",
    offHint: "-1 = 空レスポンスをそのまま返却",
  },
  stream: {
    title: "同一プロバイダー安全ウィンドウリトライ",
    where: "本プロジェクト · 出力引き渡し前",
    trigger:
      "ストリーム確立（200 受信）後に発生した問題：途中の接続切断、プロバイダーの overloaded、ストリーム内の 429 / 5xx エラーイベント——かつ呼び出し元にまだ 1 トークンも引き渡していない場合。",
    skips:
      "クォータ枯渇（402 / insufficient_quota、ローテーションで別設定へ切替）、コンテキスト長超過（413 / context length、圧縮処理へ）、400 / 401 / 403 / 404 / 422 などの確定的拒否はリトライしません。",
    desc: "同じ設定で同じリクエストを再実行します。まだ出力を一切引き渡していないため、モデルの出力やツール実行が重複することはありません。",
    attemptsLabel: "リトライ回数",
    defAttempts: 2,
    defInterval: "0.5s→1s 指数バックオフ（上限 4s）",
    offHint: "-1 = ストリーム切断時はそのまま外側のインテント再実行へ委ねる",
  },
  breaker: {
    title: "ローテーション遮断 (サーキットブレーカー)",
    where: "本プロジェクト · プロセス単位、グローバル共通",
    trigger:
      "一時的な失敗（429、5xx、ネットワークエラー）が連続して閾値に達したときに遮断します。残高不足（402）、キー無効（401 / 403）、モデル不在（404）などの確定的な失敗は閾値を問わず初回で遮断します。",
    skips: "1回でも成功すればカウントはリセットされるため、まれに不調になる程度の設定が蓄積して遮断されることはありません。",
    desc: "遮断後はクールダウンに入り、期間中はローテーションでこの設定がスキップされます。状態は DB に保存され、再起動しても失われません。",
    attemptsLabel: "遮断までの連続失敗回数",
    defAttempts: 3,
    defInterval: "1min→5min→30min 段階的",
    offHint: "-1 = 一時的な失敗では遮断しない（確定的な失敗は引き続き遮断）",
  },
  intent: {
    title: "インテント再実行",
    where: "本プロジェクト · プロセス単位、グローバル共通",
    trigger:
      "前段のすべての層でカバーしきれなかった場合：worker が model_error で終了したとき（内側のリトライをすべて使い切ったか、出力の引き渡し開始後にストリームが切断され安全に再送できない場合）。",
    skips: "クォータ枯渇はローテーションによる設定切り替えで処理されるためここでは再実行しません。タスクが一時停止 / 終了 / 終了処理に入った場合は即座に譲渡し、バックオフ時間を消費しません。",
    desc: "インテント全体を最初からやり直します。最も外側の層であるため、1回の再実行につき内側の各層の回数が掛け算で適用されます。",
    attemptsLabel: "再実行回数",
    defAttempts: 2,
    defInterval: "固定 3s",
    offHint: "-1 = 再実行せず、該当インテントを即座に blocked と判定",
  },
} satisfies Record<string, LayerMeta>;

type LayerKey = keyof typeof RETRY_LAYERS;

/** 毫秒的人话，只用于在输入框旁边回显，免得数零。 */
function humanMs(ms: number) {
  if (!Number.isFinite(ms) || ms <= 0) return "";
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${Number((ms / 1000).toFixed(2))}s`;
  return `${Number((ms / 60_000).toFixed(2))}min`;
}

/** 受控数字输入：空串 ↔ 0，中间态（"-"、"1e"）原样留在本地，不打扰父级。 */
function NumField({
  id,
  value,
  onChange,
  placeholder,
  min,
}: {
  id: string;
  value: number;
  onChange: (n: number) => void;
  placeholder: string;
  min: number;
}) {
  const [text, setText] = React.useState(value === 0 ? "" : String(value));
  // 父级换了一整套值（读取到策略、切换配置）时跟上；自己敲字时不会走到这里，
  // 因为那时 value 已经等于本地文本 parse 后的结果。
  React.useEffect(() => {
    const incoming = value === 0 ? "" : String(value);
    setText((cur) => (Number(cur || 0) === value ? cur : incoming));
  }, [value]);
  return (
    <Input
      id={id}
      type="number"
      min={min}
      className="w-28 shrink-0"
      value={text}
      placeholder={placeholder}
      onChange={(e) => {
        setText(e.target.value);
        const n = Number(e.target.value);
        onChange(e.target.value.trim() === "" || !Number.isFinite(n) ? 0 : Math.trunc(n));
      }}
    />
  );
}

/** 一层重试的两个旋钮。idPrefix 用来在同一页出现多次时保住 label 的 htmlFor。 */
export function RetryRuleFields({
  layer,
  idPrefix,
  value,
  onChange,
  compact,
}: {
  layer: LayerKey;
  idPrefix: string;
  value: LLMRetryRule;
  onChange: (r: LLMRetryRule) => void;
  /** true = 配置抽屉里的紧凑版：省掉展开说明，只留「什么错误会走到这层」这一句 */
  compact?: boolean;
}) {
  const meta = RETRY_LAYERS[layer];
  const human = humanMs(value.interval_ms);
  return (
    <div className={compact ? "grid gap-2" : "grid gap-3 rounded-lg border p-3"}>
      <div className="grid gap-0.5">
        <div className="flex flex-wrap items-baseline gap-2">
          <Label className="text-sm">{meta.title}</Label>
          <span className="text-muted-foreground text-xs">{meta.where}</span>
        </div>
        {/* 哪些错误会走到这层，具体到状态码——填了旋钮却看不到效果，多半是错误压根不落在这层。 */}
        <p className="text-muted-foreground text-xs">
          <span className="font-medium text-foreground">トリガー</span>：{meta.trigger}
        </p>
        {!compact && meta.skips && (
          <p className="text-muted-foreground text-xs">
            <span className="font-medium text-foreground">対象外</span>：{meta.skips}
          </p>
        )}
        {!compact && <p className="text-muted-foreground text-xs">{meta.desc}</p>}
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="flex items-center gap-2">
          <Label htmlFor={`${idPrefix}-${layer}-n`} className="text-muted-foreground text-xs">
            {meta.attemptsLabel}
          </Label>
          <NumField
            id={`${idPrefix}-${layer}-n`}
            min={-1}
            value={value.attempts}
            placeholder={`デフォルト ${meta.defAttempts}`}
            onChange={(n) => onChange({ ...value, attempts: n })}
          />
        </div>
        <div className="flex items-center gap-2">
          <Label htmlFor={`${idPrefix}-${layer}-ms`} className="text-muted-foreground text-xs">
            間隔 ms
          </Label>
          <NumField
            id={`${idPrefix}-${layer}-ms`}
            min={0}
            value={value.interval_ms}
            placeholder="デフォルトバックオフ"
            onChange={(n) => onChange({ ...value, interval_ms: n })}
          />
          <span className="text-muted-foreground text-xs">{human ? `固定 ${human}` : meta.defInterval}</span>
        </div>
      </div>
      {!compact && <p className="text-muted-foreground text-xs">空欄 = デフォルトを使用；{meta.offHint}。</p>}
    </div>
  );
}

/** 模型配置抽屉里的三层覆盖（跟着端点走的那三层）。 */
export function ProfileRetryFields({
  value,
  onChange,
}: {
  value: LLMRetryOverride;
  onChange: (o: LLMRetryOverride) => void;
}) {
  return (
    <div className="grid gap-3 rounded-lg border p-3">
      <div className="grid gap-0.5">
        <Label className="text-sm">リトライ上書き設定</Label>
        <p className="text-muted-foreground text-xs">
          この設定にのみ適用され、「リトライとバックオフ」のグローバルデフォルトを上書きします。各欄とも空欄 = グローバルに従う、回数に -1 = その層のリトライを無効化、間隔を入力すると指数バックオフの代わりに固定間隔を使用します。遮断（サーキットブレーカー）とインテント再実行はプロセス単位のため、グローバル設定タブでのみ調整できます。
        </p>
      </div>
      {(["connect", "empty", "stream"] as const).map((k) => (
        <div key={k} className="border-t pt-3 first:border-t-0 first:pt-0">
          <RetryRuleFields
            compact
            layer={k}
            idPrefix="pf"
            value={value[k]}
            onChange={(r) => onChange({ ...value, [k]: r })}
          />
        </div>
      ))}
    </div>
  );
}

/** 「重试与退避」tab：五层的全局默认值。 */
export function RetryPolicyPanel() {
  const [policy, setPolicy] = React.useState<LLMRetryPolicy>(ZERO_POLICY);
  const [loading, setLoading] = React.useState(true);
  const [saving, setSaving] = React.useState(false);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const p = await api.llmRetryPolicy();
      setPolicy({ ...ZERO_POLICY, ...p });
    } catch (e) {
      toast.error(`リトライポリシーの読み込みに失敗しました：${(e as Error).message}`);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  async function save() {
    if (saving) return;
    setSaving(true);
    try {
      // 后端会把越界值夹回区间并回传，直接用回传值刷新，所见即所存。
      const saved = await api.saveLLMRetryPolicy(policy);
      setPolicy({ ...ZERO_POLICY, ...saved });
      toast.success("保存しました。即座に反映されます（現在実行中の呼び出しは旧パラメータのままです）");
    } catch (e) {
      toast.error(`保存に失敗しました：${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  const set = (k: LayerKey) => (r: LLMRetryRule) => setPolicy((p) => ({ ...p, [k]: r }));

  if (loading) {
    return (
      <div className="flex items-center gap-2 rounded-lg border border-dashed p-10 text-muted-foreground text-sm">
        <Loader2Icon className="size-4 animate-spin" /> リトライポリシーを読み込み中…
      </div>
    );
  }

  return (
    <div className="grid gap-4">
      <div className="rounded-lg border bg-muted/30 p-3 text-muted-foreground text-xs leading-relaxed">
        モデル呼び出しの失敗は、内側から外側へ順に 5 層のリトライを経由します：
        <span className="text-foreground"> 接続 → 空レスポンス → 同一プロバイダー安全ウィンドウ → ローテーション遮断 → インテント再実行</span>
        。内側のリトライを使い切って初めて外側に回るため、試行回数は
        <span className="text-foreground">掛け算</span>
        になります——すべての層を大きく設定すると、1 回の不調で数十回のリクエストが消費される可能性があります。
        すべて空欄にすると現在のデフォルト値となり、初期状態と同じ動作になります。最初の 3 層は各モデル設定で個別に上書き可能です。
      </div>

      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        {(Object.keys(RETRY_LAYERS) as LayerKey[]).map((k) => (
          <RetryRuleFields key={k} layer={k} idPrefix="gl" value={policy[k]} onChange={set(k)} />
        ))}
      </div>

      <div className="flex gap-2">
        <Button onClick={save} disabled={saving}>
          {saving ? <Loader2Icon className="animate-spin" /> : <SaveIcon />}
          保存
        </Button>
        <Button variant="outline" onClick={() => setPolicy(ZERO_POLICY)} disabled={saving}>
          すべてデフォルトに戻す
        </Button>
      </div>
    </div>
  );
}
