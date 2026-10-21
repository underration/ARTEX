"use client";

import * as React from "react";

import { CpuIcon, FlaskConicalIcon, KeyboardIcon, RadioTowerIcon, SearchIcon, ShieldAlertIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";
import { CHAT_SEND_MODE_OPTIONS, type ChatSendMode, setChatSendMode, useChatSendMode } from "@/lib/chat-send-mode";
import type { Settings } from "@/lib/types";

import { UpdateCard } from "./_components/update-card";

export default function SystemSettingsPage() {
  const [trafficCapture, setTrafficCapture] = React.useState(false);
  const [agentTrafficBinding, setAgentTrafficBinding] = React.useState(false);
  const [webSearch, setWebSearch] = React.useState(false);
  const [backend, setBackend] = React.useState("ddgs");
  const [braveKeySet, setBraveKeySet] = React.useState(false);
  const [braveKeyInput, setBraveKeyInput] = React.useState("");
  const [tavilyKeySet, setTavilyKeySet] = React.useState(false);
  const [tavilyKeyInput, setTavilyKeyInput] = React.useState("");
  const [savingTavilyKey, setSavingTavilyKey] = React.useState(false);
  const [proxyInput, setProxyInput] = React.useState("");
  const [savingProxy, setSavingProxy] = React.useState(false);
  const [globalProxyInput, setGlobalProxyInput] = React.useState("");
  const [savingGlobalProxy, setSavingGlobalProxy] = React.useState(false);
  const [testing, setTesting] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [savingKey, setSavingKey] = React.useState(false);
  const [pyInterp, setPyInterp] = React.useState("");
  const [workers, setWorkers] = React.useState("3");
  const [savingWorkers, setSavingWorkers] = React.useState(false);
  // 操作约束注入范围(默认都开)。
  const [injectPlanner, setInjectPlanner] = React.useState(true);
  const [injectWorker, setInjectWorker] = React.useState(true);
  // 实验功能:noa 上下文压缩(默认关)。
  const [noaCompaction, setNoaCompaction] = React.useState(false);
  // 纯前端偏好：不走 /api/settings，直接读写 localStorage。
  const sendMode = useChatSendMode();

  const apply = React.useCallback((s: Settings) => {
    setTrafficCapture(!!s.traffic_capture);
    setAgentTrafficBinding(!!s.agent_traffic_binding);
    setWebSearch(!!s.web_search_enabled);
    setBackend(s.web_search_backend || "ddgs");
    setBraveKeySet(!!s.brave_key_set);
    setTavilyKeySet(!!s.tavily_key_set);
    setProxyInput(s.web_search_proxy ?? "");
    setGlobalProxyInput(s.global_proxy ?? "");
    setPyInterp(s.python_interpreter ?? "");
    setWorkers(String(s.workers ?? 3));
    setInjectPlanner(s.constraints_inject_planner !== false);
    setInjectWorker(s.constraints_inject_worker !== false);
    setNoaCompaction(!!s.noa_compaction);
  }, []);

  const saveWorkers = () => {
    const n = Number(workers);
    if (!Number.isInteger(n) || n <= 0) {
      toast.error("同時実行数は 0 より大きい整数である必要があります");
      return;
    }
    setSavingWorkers(true);
    api
      .setSettings({ workers: n })
      .then((s) => {
        apply(s);
        toast.success("同時実行 Work Agent 数を保存しました（以降に開始するタスクから有効になります）");
      })
      .catch((e) => toast.error("保存に失敗しました：" + (e as Error).message))
      .finally(() => setSavingWorkers(false));
  };

  const savePython = () => {
    setSaving(true);
    api
      .setSettings({ python_interpreter: pyInterp.trim() })
      .then((s) => {
        apply(s);
        toast.success("Python インタープリター設定を保存しました");
      })
      .catch((e) => toast.error("保存に失敗しました：" + (e as Error).message))
      .finally(() => setSaving(false));
  };
  const detectPython = () => {
    setSaving(true);
    api
      .detectPython()
      .then((r) => setPyInterp(r.python_interpreter))
      .catch(() => undefined)
      .finally(() => setSaving(false));
  };

  React.useEffect(() => {
    api
      .settings()
      .then(apply)
      .catch(() => undefined)
      .finally(() => setLoaded(true));
  }, [apply]);

  const toggleTraffic = (v: boolean) => {
    setTrafficCapture(v); // optimistic
    setSaving(true);
    api
      .setSettings({ traffic_capture: v })
      .then(apply)
      .catch(() => setTrafficCapture(!v)) // revert on failure
      .finally(() => setSaving(false));
  };

  const toggleInjectPlanner = (v: boolean) => {
    setInjectPlanner(v); // optimistic
    api
      .setSettings({ constraints_inject_planner: v })
      .then(apply)
      .catch(() => setInjectPlanner(!v)); // revert on failure
  };

  const toggleAgentTrafficBinding = (v: boolean) => {
    setAgentTrafficBinding(v);
    setSaving(true);
    api
      .setSettings({ agent_traffic_binding: v })
      .then((s) => {
        apply(s);
        toast.success(v ? "Agent トラフィック自動紐付けを有効にしました" : "Agent トラフィック自動紐付けを無効にしました");
      })
      .catch((e) => {
        setAgentTrafficBinding(!v);
        toast.error(`保存に失敗しました：${(e as Error).message}`);
      })
      .finally(() => setSaving(false));
  };

  const toggleInjectWorker = (v: boolean) => {
    setInjectWorker(v); // optimistic
    api
      .setSettings({ constraints_inject_worker: v })
      .then(apply)
      .catch(() => setInjectWorker(!v)); // revert on failure
  };

  const toggleNoaCompaction = (v: boolean) => {
    setNoaCompaction(v); // optimistic
    api
      .setSettings({ noa_compaction: v })
      .then((s) => {
        apply(s);
        toast.success(v ? "noa コンテキスト圧縮を有効にしました（以降に開始する実行から有効になります）" : "noa コンテキスト圧縮を無効にしました（組み込み圧縮に戻ります）");
      })
      .catch((e) => {
        setNoaCompaction(!v); // revert on failure
        toast.error(`保存に失敗しました：${(e as Error).message}`);
      });
  };

  // Persist a web-search patch (enable and/or backend). Optimistic with refetch.
  const saveWebSearch = (patch: Partial<Settings>) => {
    setSaving(true);
    api
      .setSettings(patch)
      .then((s) => {
        apply(s);
        toast.success("Web 検索設定を保存しました");
      })
      .catch((e) => {
        toast.error("保存に失敗しました：" + (e as Error).message);
        api
          .settings()
          .then(apply)
          .catch(() => undefined);
      })
      .finally(() => setSaving(false));
  };

  const saveBraveKey = () => {
    setSavingKey(true);
    api
      .setSettings({ brave_search_api_key: braveKeyInput })
      .then((s) => {
        apply(s);
        setBraveKeyInput("");
        toast.success("Brave API Key を保存しました");
      })
      .catch((e) => toast.error("保存に失敗しました：" + (e as Error).message))
      .finally(() => setSavingKey(false));
  };

  const saveTavilyKey = () => {
    setSavingTavilyKey(true);
    api
      .setSettings({ tavily_search_api_key: tavilyKeyInput })
      .then((s) => {
        apply(s);
        setTavilyKeyInput("");
        toast.success("Tavily API Key を保存しました");
      })
      .catch((e) => toast.error("保存に失敗しました：" + (e as Error).message))
      .finally(() => setSavingTavilyKey(false));
  };

  const saveProxy = () => {
    setSavingProxy(true);
    api
      .setSettings({ web_search_proxy: proxyInput.trim() })
      .then((s) => {
        apply(s);
        toast.success(proxyInput.trim() ? "出力プロキシを保存しました" : "出力プロキシをクリアしました（直接接続に変更）");
      })
      .catch((e) => toast.error("保存に失敗しました：" + (e as Error).message))
      .finally(() => setSavingProxy(false));
  };

  const saveGlobalProxy = () => {
    setSavingGlobalProxy(true);
    api
      .setSettings({ global_proxy: globalProxyInput.trim() })
      .then((s) => {
        apply(s);
        toast.success(globalProxyInput.trim() ? "グローバルプロキシを保存しました" : "グローバルプロキシをクリアしました（直接接続に変更）");
      })
      .catch((e) => toast.error("保存に失敗しました：" + (e as Error).message))
      .finally(() => setSavingGlobalProxy(false));
  };

  // Run a real "test" search ("test") against the CURRENT form values (backend +
  // proxy + entered key), falling back to saved values server-side. Toasts result.
  const runTest = () => {
    setTesting(true);
    api
      .testWebSearch({
        web_search_backend: backend,
        web_search_proxy: proxyInput.trim(),
        brave_search_api_key: braveKeyInput,
        tavily_search_api_key: tavilyKeyInput,
      })
      .then((r) => {
        if (r.ok) toast.success(`検索テスト成功 · ${r.backend} が ${r.count} 件の結果を返しました`);
        else toast.error("検索テスト失敗：" + (r.error || "不明なエラー"));
      })
      .catch((e) => toast.error("検索テスト失敗：" + (e as Error).message))
      .finally(() => setTesting(false));
  };

  // brave-free selected but no key stored and none being entered → tool stays off.
  const braveNeedsKey = webSearch && backend === "brave-free" && !braveKeySet;

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">システム設定</h1>
        <p className="text-muted-foreground text-sm">グローバルランタイムスイッチ</p>
      </div>

      {/* 多列而非 grid：网络搜索卡片比其余高数倍，且高度随所选后端变化（brave/tavily
          的 key 输入是条件渲染）。grid 会按最高的一张撑满整行、在旁边留下大片空白，
          多列则自动按内容高度平衡填充。卡片间距靠 mb 而非 gap——多列布局下
          column-gap 只管列间距，行间距要由子元素自己给。 */}
      <div className="columns-1 gap-4 md:gap-6 lg:columns-2">
        <UpdateCard />

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              トラフィックキャプチャ
            </CardTitle>
            <CardDescription>
              有効にすると、すべての Agent の HTTP トラフィックが記録プロキシ経由で全量 DB に保存され、Agent に traffic_search / traffic_get
              ツールとプロキシ設定（プロンプト内のプロキシ説明を含む）が注入されます。
              <br />
              無効（デフォルト）時はトラフィックを一切記録しません：Agent
              にはプロキシ設定やトラフィックツールが<b>渡されず</b>、プロンプトにもプロキシ関連の内容は<b>含まれません</b>。切り替え後は Agent が即座に再構築され反映されます。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="traffic-capture" className="text-sm font-normal text-muted-foreground">
              {trafficCapture ? "有効 · トラフィックを記録しプロキシを注入中" : "無効 · 記録せずプロキシも注入しない"}
            </Label>
            <Switch
              id="traffic-capture"
              checked={trafficCapture}
              disabled={!loaded || saving}
              onCheckedChange={toggleTraffic}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              Agent トラフィック自動紐付け
            </CardTitle>
            <CardDescription id="agent-traffic-binding-description">
              デフォルトは無効。有効にすると、脆弱性の DB 保存時に起動するレポート Agent が既存の HTTP リクエスト/レスポンスを照合し、該当するトラフィックを関連付けてからレポートを作成します。
              <b>パケットの参照および追加のツール呼び出しにより Token 消費量が増加します。</b>
              <br />
              TCP、未キャプチャ、または一致するトラフィックがない場合でも通常どおり報告可能です。このスイッチはトラフィックキャプチャ、手動紐付け、および保存済みエビデンスの閲覧には影響しません。次のラウンドの Agent
              から有効になり、無効にすると新しい自動紐付けは即座に拒否されます。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="agent-traffic-binding" className="text-sm font-normal text-muted-foreground">
              {agentTrafficBinding ? "有効 · Token 消費量が増加します" : "無効 · 手動での紐付けは引き続き可能"}
            </Label>
            <Switch
              id="agent-traffic-binding"
              aria-describedby="agent-traffic-binding-description"
              checked={agentTrafficBinding}
              disabled={!loaded || saving}
              onCheckedChange={toggleAgentTrafficBinding}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              グローバルプロキシ
            </CardTitle>
            <CardDescription>
              すべての Agent の<b>ターゲット向けトラフィック</b>がこのプロキシを経由して外部へ出ます（送信元 IP の秘匿 / 踏み台経由）。<b>http / https / socks5</b> に対応し、{" "}
              <code>user:pass</code> 認証も指定可能です。空欄 = 直接接続。
              <br />
              <b>トラフィックキャプチャ</b>が有効な場合、このプロキシは記録プロキシの<b>上流（アップストリーム）</b>として動作します（トラフィックは全量 DB に保存された上で、このプロキシを経由して外部へ出ます）。キャプチャが無効な場合は、Agent の bash / WebFetch に直接注入されます。Web 検索プロキシや LLM プロキシとは独立しています。
              <br />
              <b>ヒント</b>：<b>キャプチャ無効</b>時の socks5 は各コマンドラインツールの <code>ALL_PROXY</code> 対応状況に依存します（curl
              は利用可能ですが、一部のツールは無視する場合があります）。主に socks5 を使用する場合は、トラフィックキャプチャを有効にすることをお勧めします——MITM
              プロキシ自身が接続を行うため、ツール側に意識させることなく確実に適用されます。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-2">
            <Label htmlFor="global-proxy" className="text-sm font-normal text-muted-foreground">
              プロキシアドレス
            </Label>
            <div className="flex items-center gap-2">
              <Input
                id="global-proxy"
                autoComplete="off"
                placeholder="socks5://user:pass@host:1080 または http://host:port（空欄=直接接続）"
                value={globalProxyInput}
                disabled={!loaded || savingGlobalProxy}
                onChange={(e) => setGlobalProxyInput(e.target.value)}
              />
              <Button type="button" onClick={saveGlobalProxy} disabled={!loaded || savingGlobalProxy}>
                保存
              </Button>
            </div>
            <p className="text-muted-foreground text-xs">
              {globalProxyInput.trim() ? "設定済み · すべてのターゲット向けトラフィックがこのプロキシを経由します" : "未設定 · ターゲット向けトラフィックは直接接続されます"}
            </p>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <ShieldAlertIcon className="size-4" />
              操作制約の注入
            </CardTitle>
            <CardDescription>
              有効にすると、各タスクの<b>操作制約</b>（タスク概要の「操作制約」で管理される allow/deny 項目）を対応する Agent
              のシステムプロンプトに組み込み、探索範囲を制限します（例：「現在のポートのみテスト」「ブルートフォース禁止」）。
              <br />
              <b>プランナー（planner）</b>と<b>ワーカー（worker）</b>への注入を個別に制御できます（デフォルトは両方オン）。切り替えは即座に反映され（次のラウンドで読み込み）、Agent の再構築は不要です。オフにすると該当 Agent には制約が表示されなくなります。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="inject-planner" className="text-sm font-normal text-muted-foreground">
                プランナー（planner）へ注入{injectPlanner ? " · 有効" : " · 無効"}
              </Label>
              <Switch
                id="inject-planner"
                checked={injectPlanner}
                disabled={!loaded}
                onCheckedChange={toggleInjectPlanner}
              />
            </div>
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="inject-worker" className="text-sm font-normal text-muted-foreground">
                ワーカー（worker）へ注入{injectWorker ? " · 有効" : " · 無効"}
              </Label>
              <Switch
                id="inject-worker"
                checked={injectWorker}
                disabled={!loaded}
                onCheckedChange={toggleInjectWorker}
              />
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <FlaskConicalIcon className="size-4" />
              実験的機能
            </CardTitle>
            <CardDescription>
              検証中の機能であり、デフォルトは無効です。Agent の挙動が変わったり安定性に影響したりする可能性があるため、影響を理解した上で有効にしてください。
              <br />
              <b>noa コンテキスト圧縮</b>：モデル自身が長い対話履歴を能動的に圧縮します（norma v0.4.0）。有効にすると、プラットフォーム上の 4 種類の Agent（
              <b>プランナー / ワーカー / メイン Agent / チャット</b>）が組み込み圧縮の代わりに noa でコンテキストを管理するようになり、圧縮前の原文は振り返り用にタスクの作業ディレクトリへアーカイブされます。切り替えは即座に反映され（以降に開始する実行から有効）、Agent の再構築は不要です。無効にすると即座に組み込み圧縮へ戻ります。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="noa-compaction" className="text-sm font-normal text-muted-foreground">
              noa コンテキスト圧縮{noaCompaction ? " · 有効" : " · 無効"}
            </Label>
            <Switch
              id="noa-compaction"
              checked={noaCompaction}
              disabled={!loaded}
              onCheckedChange={toggleNoaCompaction}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <SearchIcon className="size-4" />
              Web 検索
            </CardTitle>
            <CardDescription>
              Web 検索の<b>全体スイッチ + ソース設定</b>です。ここを有効にして初めて、<b>各 Agent の設定</b>で個別に
              <b>web_search</b>（タイトル/リンク/要約のみを返し、本文は取得しません。本文取得は WebFetch が担当）を有効化できるようになります。Web 検索は記録プロキシを<b>経由せず</b>、トラフィックキャプチャとは独立しています。
              <br />
              ソースは <b>DuckDuckGo（ddgs）</b>（Key 不要）、<b>Brave（無料版）</b>（Brave API Key が必要）、{" "}
              <b>Tavily</b>（Tavily API Key が必要）、または <b>DeepSeek</b>（現在の LLM 設定を再利用）から選択できます。全体スイッチが無効の場合、各
              Agent の Web 検索スイッチは使用できません。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="web-search" className="text-sm font-normal text-muted-foreground">
                {webSearch ? "全体スイッチ有効 · 各 Agent 設定で個別に有効化可能" : "無効 · 各 Agent で Web 検索を有効化できません"}
              </Label>
              <Switch
                id="web-search"
                checked={webSearch}
                disabled={!loaded || saving}
                onCheckedChange={(v) => {
                  setWebSearch(v); // optimistic
                  saveWebSearch({ web_search_enabled: v });
                }}
              />
            </div>

            {webSearch && (
              <div className="flex items-center justify-between gap-4">
                <Label className="text-sm font-normal text-muted-foreground">検索ソース</Label>
                <Select
                  value={backend}
                  disabled={!loaded || saving}
                  onValueChange={(v) => {
                    setBackend(v); // optimistic
                    saveWebSearch({ web_search_backend: v });
                  }}
                >
                  <SelectTrigger className="w-48 shrink-0">
                    <SelectValue placeholder="ソースを選択" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="ddgs">DuckDuckGo（ddgs · 無料・Key 不要）</SelectItem>
                    <SelectItem value="brave-free">Brave（無料版 · Key 必要）</SelectItem>
                    <SelectItem value="tavily">Tavily（Key 必要）</SelectItem>
                    <SelectItem value="deepseek">DeepSeek（公式）</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            )}

            {webSearch && backend === "deepseek" && (
              <div className="border-border/60 bg-muted/30 flex flex-col gap-2 rounded-md border p-3">
                <p className="text-sm font-medium">DeepSeek 公式 Web 検索</p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  このソースは<b>現在アクティブな LLM 設定</b>をそのまま再利用します。そのため、
                  <b>DeepSeek 公式モデルのみをサポート</b>し、かつその設定が<b>必ず anthropic プロトコルを使用している必要があります</b>
                  （DeepSeek の OpenAI プロトコルエンドポイントはサーバーサイド検索に対応していません）。LLM 設定を切り替えるとこのソースが機能しなくなる場合があります。
                </p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  他のソースとは異なり、検索は <b>DeepSeek サーバー側で実行</b>されます：検索ごとにモデル呼び出しが 1 回追加消費され（Token
                  料金が発生）、検索リクエストは<b>上記の出力プロキシを経由せず</b>、<b>トラフィック記録にも残りません</b>。返却される結果は<b>タイトルとリンクのみ</b>
                  （要約なし）で、本文が必要な場合は WebFetch で取得します。
                </p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  上記の条件を満たしているかはご自身で確認してください（システム側でのブロックは行いません）。下の「検索テスト」ボタンで実際に 1 回実行して検証できます。
                </p>
              </div>
            )}

            {webSearch && backend === "brave-free" && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="brave-key" className="text-sm font-normal text-muted-foreground">
                  Brave Search API Key
                  {braveKeySet && <span className="ml-2 text-xs text-emerald-500">設定済み</span>}
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="brave-key"
                    type="password"
                    autoComplete="off"
                    placeholder={braveKeySet ? "設定済み（空欄で変更なし）" : "Brave API Key を入力"}
                    value={braveKeyInput}
                    disabled={!loaded || savingKey}
                    onChange={(e) => setBraveKeyInput(e.target.value)}
                  />
                  <Button
                    type="button"
                    onClick={saveBraveKey}
                    disabled={!loaded || savingKey || braveKeyInput.trim() === ""}
                  >
                    保存
                  </Button>
                </div>
                {braveNeedsKey && (
                  <p className="text-xs text-amber-500">
                    Brave が選択されていますが Key が未設定です —— Key を保存するまで検索ツールは有効になりません。
                  </p>
                )}
                <p className="text-muted-foreground text-xs">
                  無料枠は約 2,000 回/月です。https://brave.com/search/api/ から Key を取得してください。
                </p>
              </div>
            )}

            {webSearch && backend === "tavily" && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="tavily-key" className="text-sm font-normal text-muted-foreground">
                  Tavily Search API Key
                  {tavilyKeySet && <span className="ml-2 text-xs text-emerald-500">設定済み</span>}
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="tavily-key"
                    type="password"
                    autoComplete="off"
                    placeholder={tavilyKeySet ? "設定済み（空欄で変更なし）" : "Tavily API Key を入力（tvly-…）"}
                    value={tavilyKeyInput}
                    disabled={!loaded || savingTavilyKey}
                    onChange={(e) => setTavilyKeyInput(e.target.value)}
                  />
                  <Button
                    type="button"
                    onClick={saveTavilyKey}
                    disabled={!loaded || savingTavilyKey || tavilyKeyInput.trim() === ""}
                  >
                    保存
                  </Button>
                </div>
                {webSearch && backend === "tavily" && !tavilyKeySet && (
                  <p className="text-xs text-amber-500">
                    Tavily が選択されていますが Key が未設定です —— Key を保存するまで検索ツールは有効になりません。
                  </p>
                )}
                <p className="text-muted-foreground text-xs">https://tavily.com で登録して API Key を取得してください。</p>
              </div>
            )}

            {webSearch && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="ws-proxy" className="text-sm font-normal text-muted-foreground">
                  出力プロキシ（任意）
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="ws-proxy"
                    autoComplete="off"
                    placeholder="http://host:port または socks5://host:port（空欄=直接接続）"
                    value={proxyInput}
                    disabled={!loaded || savingProxy}
                    onChange={(e) => setProxyInput(e.target.value)}
                  />
                  <Button type="button" onClick={saveProxy} disabled={!loaded || savingProxy}>
                    保存
                  </Button>
                </div>
                <p className="text-muted-foreground text-xs">
                  検索エンドポイントへのアクセス専用プロキシ（VPN/SOCKS など）。トラフィック記録用の MITM プロキシとは無関係で、直接アクセスできない環境でこのプロキシを経由します。
                </p>
              </div>
            )}

            {webSearch && (
              <div className="flex items-center justify-between gap-4 border-t pt-4">
                <p className="text-muted-foreground text-xs">
                  現在の設定（ソース + プロキシ + Key）で実際に「test」を 1 回検索し、利用可能か検証します。
                </p>
                <Button
                  type="button"
                  variant="outline"
                  onClick={runTest}
                  disabled={!loaded || testing}
                  className="shrink-0"
                >
                  {testing ? "テスト中…" : "検索テスト"}
                </Button>
              </div>
            )}
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              カスタムスクリプト · Python インタープリター
            </CardTitle>
            <CardDescription>
              カスタム <b>script</b> タイプのツールで Python を実行する際に使用します。起動時に自動検出（python3 優先）されますが、ここで venv や特定バージョンの絶対パスを手動指定することもできます（空欄の場合は実行時に自動検出）。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Input
                className="font-mono text-sm"
                placeholder="/usr/bin/python3（空欄=自動検出）"
                value={pyInterp}
                disabled={!loaded || saving}
                onChange={(e) => setPyInterp(e.target.value)}
              />
              <Button variant="outline" onClick={detectPython} disabled={!loaded || saving}>
                再検出
              </Button>
              <Button onClick={savePython} disabled={!loaded || saving}>
                保存
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <CpuIcon className="size-4" />
              同時実行数 · Work Agent 数
            </CardTitle>
            <CardDescription>
              各タスクで同時実行する Work Agent の数（デフォルト 3）。数値が大きいほど並行調査が増えますが、消費量も増加します。変更後は
              <b>以降に開始するタスクから有効</b>になり、実行中のタスクには影響しません。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Input
                type="number"
                min={1}
                className="w-32 font-mono text-sm"
                placeholder="3"
                value={workers}
                disabled={!loaded || savingWorkers}
                onChange={(e) => setWorkers(e.target.value)}
              />
              <Button onClick={saveWorkers} disabled={!loaded || savingWorkers}>
                保存
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <KeyboardIcon className="size-4" />
              チャット入力欄の送信キー設定
            </CardTitle>
            <CardDescription>
              チャットページとタスク詳細のメイン Agent チャット入力欄で共通の設定です。選択すると即座に反映され、保存操作は不要です。
              <br />
              この設定は<b>このブラウザにのみ保存</b>され、アカウントには同期されません。ブラウザを変更したりサイトデータを消去した場合は再設定が必要です。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="chat-send-mode" className="text-sm font-normal text-muted-foreground">
              送信方法
            </Label>
            <Select value={sendMode} onValueChange={(v) => setChatSendMode(v as ChatSendMode)}>
              <SelectTrigger id="chat-send-mode" className="w-72">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CHAT_SEND_MODE_OPTIONS.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
