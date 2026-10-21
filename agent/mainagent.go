package agent

import (
	"context"
	"fmt"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// MainAgent is the thin human-interface orchestrator (docs §4.2 / §7). The human
// chats with it; it observes (read tools), and steers by injecting hints
// (→planner) or direct high-priority intents (→frontier). It does NOT run the
// autonomous intent-generation loop (that is the planner's job).
type MainAgent struct {
	findingRecorder FindingRecorder
	prov            llm.Provider
	model           string
	tx              *transcript.Store                      // raw LLM conversation persistence (nil = off)
	window          int                                    // context window in tokens (for compaction)
	windowFn        func() int                             // optional dynamic task-chain minimum
	maxTurns        int                                    // max agent turns per run (0 = unlimited)
	proxyAddr       string                                 // recording proxy for WebFetch (empty = direct)
	proxyCACert     string                                 // recording proxy's CA cert path (HTTPS verify)
	webSearch       WebSearchOpts                          // web_search tool backend selection (off by default)
	workDir         string                                 // shared work dir (surfaced in prompt as artifact-output target)
	steerWork       func(intentID int64, msg string) error // engine callback: steer a running work (nil = off)
	nonStreamingFn  func() bool                            // resolver: use non-streaming (Complete) path? (nil = streaming)
	noaEnabledFn    func() bool                            // resolver: use experimental noa compaction? (nil = off)
	maxTokensFn     func() int                             // resolver: per-reply output cap (nil/0 = send no cap)
}

// SetNoaEnabled wires a resolver deciding whether runs use the experimental noa
// context-compression mechanism. nil/unset = off (built-in compaction). Read per
// run so the settings toggle takes effect without rebuilding the agent.
func (m *MainAgent) SetNoaEnabled(fn func() bool) { m.noaEnabledFn = fn }

// SetNonStreaming wires a resolver deciding whether runs use the non-streaming
// model path (true = non-streaming). nil/unset = streaming (default).
func (m *MainAgent) SetNonStreaming(fn func() bool) { m.nonStreamingFn = fn }

func (m *MainAgent) nonStreaming() bool { return m.nonStreamingFn != nil && m.nonStreamingFn() }

// SetMaxTokens wires a resolver for the per-reply output cap. nil/unset or 0 =
// send no cap and let the endpoint decide. Read per run, like nonStreaming.
func (m *MainAgent) SetMaxTokens(fn func() int) { m.maxTokensFn = fn }

func (m *MainAgent) maxTokens() int {
	if m.maxTokensFn == nil {
		return 0
	}
	return m.maxTokensFn()
}

func NewMainAgent(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int) *MainAgent {
	return &MainAgent{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns}
}

func (m *MainAgent) SetCompactionWindowResolver(fn func() int) { m.windowFn = fn }

func (m *MainAgent) compactionWindow() int {
	if m.windowFn != nil {
		return m.windowFn()
	}
	return m.window
}

// SetProxy points the main agent's WebFetch at the recording proxy plus the CA
// cert it trusts to verify HTTPS through it (empty addr = direct).
func (m *MainAgent) SetProxy(addr, caCert string) { m.proxyAddr, m.proxyCACert = addr, caCert }

// SetWebSearch selects the web_search backend for the main agent (off by default).
func (m *MainAgent) SetWebSearch(o WebSearchOpts) { m.webSearch = o }

// SetSteerWork wires the engine callback that lets the main agent's steer_work
// tool inject a mid-run course-correction into a running work (nil = tool off).
func (m *MainAgent) SetSteerWork(fn func(intentID int64, msg string) error) { m.steerWork = fn }

// mainAgentDefaultTmpl is the built-in EDITABLE body (段 [A]) of the main agent
// prompt, seeded into agent_prompts. Goal is a {{.Goal}} template var; the 中间
// 产物输出规约 tail is code-owned (artifactSpec), appended after rendering.
const mainAgentDefaultTmpl = `あなたは承認済みペネトレーションテストシステムの「メイン agent」であり、人間のオペレーターとのインターフェースです。あなた自身は直接探索を行わず、自律的に連続してインテントを生成することもありません（それはプランナーの役割です）。あなたの役割は以下の通りです：

1. 観察：graph_overview / list_findings / list_facts / list_assets / get_worker_output を使用して、現在の進捗に関する人間からの質問に回答します。
2. 操舵（人間の意図をシステムに反映させる）：
   - 人間が「方向性を変えたい/特定の脆弱性クラスを重視したい/特定の領域に注力したい」場合 → add_hint でヒントを書き込みます（次回プランナーが読み取ります）。
   - 人間が「今すぐ特定のターゲットをテストしたい」場合 → add_intent で高優先度のインテント（priority 8-10）を直接注入します。システムは完了済みのタスクを自動的に実行状態へ戻し、worker にこのインテントを取得・実行させ、完了すると再び完了状態に戻ります。
     **タスク目標がすでにすべて達成されている場合**（graph_overview 内の goals がすべて met の場合）：発行前に、このインテントの背後に「達成すべき新しい結果（目標）」が暗に含まれているかを判断してください。含まれている場合は、推測した目標を 1 文で人間に復唱し、**正式な目標として登録するかどうかを聞き返してください**——人間が登録を望む場合 → set_goals で登録します（タスクは通常の計画フェーズに入り、プランナーが自律的に進行させます）；人間が望まない / 一時的に少し調べたいだけの場合 → add_intent でこの 1 件だけを発行します（worker の実行が終わればタスクは完了状態に戻り、自律的には継続しません）。このインテントが明らかに単発の確認であり新しい目標を含まない場合は、毎回聞き返す必要はなく、そのまま add_intent を呼び出してください。
   - 人間が「現在実行中のインテント (work) に対してリアルタイムに軌道修正したい（X はやめて Y に集中せよ）」場合 → steer_work を使用します（中断せず既存の進捗も失われず、worker の次のアクション前に反映されます）。まず get_worker_output で現在の動作を確認してください。方向性自体が完全に間違っている場合は、add_intent で新しいインテントを別途発行してください。
   - 人間が「達成すべき最終目標を新しく追加したい」場合 → set_goals で目標を追加します。システムはその目標をタスクグラフに書き込み、**完了済み/一時停止中のタスクを自動的に実行状態に戻して継続実行します**（その後プランナーがこれに基づいて達成状況を再度判定するため、人間が手動で再開ボタンを押す必要はありません）。
   - 人間が「テスト制約を追加/変更したい（『現在のポートのみテスト』『ブルートフォース禁止』『パッシブ偵察のみ』など、特定の操作の許可/禁止）」場合 → set_constraints で登録します（type=allow 許可 / type=deny 禁止）。制約は次の計画ラウンドで planner/worker のプロンプトに注入され、探索の境界を規定します（概要ページの「制約管理」からも追加・削除・変更が可能です）。
3. 分かりやすい言葉で簡潔に返答し、何を行ったかを説明してください。

現在のタスク目標：{{.Goal}}

検出結果を捏造してはなりません。ツールが返した実際のデータのみに基づいて回答してください。`

func mainAgentSystem(goal, dataDir, workDir string) string {
	body := renderSystem("mainagent", mainAgentDefaultTmpl, MainVars{Goal: goal, DataDir: dataDir, Now: nowStr()})
	return body + artifactSpec(workDir)
}

// Chat handles one human message and returns the assistant reply. emit, if
// non-nil, receives each execution step (thinking / tool_use / tool_result /
// text / result) so the main-agent session shows its work — exactly like the
// worker/planner sessions — not just the final answer.
func (m *MainAgent) Chat(ctx context.Context, taskID int64, mainSeg int, as *db.AssetStore, ts *db.ExplorationStore, goal, message string, emit func(db.Activity), notify, resume func(), notifyGoal, notifyHint func([]string)) (string, error) {
	tsx := NewToolSet(ts, "human")
	tsx.SetFindingRecorder(m.findingRecorder)
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetTaskID(taskID)
	tsx.SetCoverageEnabled(as == nil || as.CoverageEnabled(taskID))
	tsx.SetNotify(notify)         // 通用唤醒（无专用回调的写操作走它，debounced）
	tsx.SetResumeTask(resume)     // set_goals 新增目标 → 把已完成/暂停的任务拉回 running
	tsx.SetNotifyGoal(notifyGoal) // set_goals 新增目标 → 给 planner 记一条「人新增了目标：…」触发
	tsx.SetNotifyHint(notifyHint) // add_hint 新增提示 → 给 planner 记一条「人新增了 N 条战略提示：…」触发
	tsx.steerWork = m.steerWork   // enable steer_work tool (nil = unavailable)
	// 领域工具 + 基础默认工具集（Read/Write/Edit/MultiEdit/LS/Glob/Grep/Bash）
	// 资产覆盖度功能关闭时剔除 add_task_scope/list_untested_assets（不入 prompt）。
	base := append(tsx.DropCoverageTools(tsx.MainAgentTools()), actool.DefaultTools()...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts)})
	tools, def, cleanup := AugmentTools(ctx, "mainagent", base)
	defer cleanup()
	// 本任务的工作目录 <workDir>/tasks/<taskID>，先建好。
	mainDir := ensureRunDir(m.workDir, taskID, 0)
	ctx = intercept.WithReviewWorkingDirectory(ctx, mainDir)
	system, boundary := deferredSystem(mainAgentSystem(goal, m.workDir, mainDir), def)
	opts := agentcore.Options{
		Provider:        m.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		EnableWebFetch:  true, // 走记录代理留痕；载入代理 CA 验证 MITM 重签的 HTTPS 证书
		WebFetchProxy:   m.proxyAddr,
		WebFetchCACert:  m.proxyCACert,
		// 联网搜索(可选)。ddgs 无需 key；brave-free 需 BraveKey；tavily 需 TavilyKey。
		// WebSearchProxy 是独立出口代理(http/https/socks5)，与记录流量的 MITM 代理无关；空则直连。
		EnableWebSearch:       m.webSearch.Enabled,
		WebSearchBackend:      m.webSearch.Backend,
		BraveSearchAPIKey:     m.webSearch.BraveKey,
		TavilySearchAPIKey:    m.webSearch.TavilyKey,
		DeepSeekSearchBaseURL: m.webSearch.DeepSeekBaseURL,
		DeepSeekSearchAPIKey:  m.webSearch.DeepSeekAPIKey,
		DeepSeekSearchModel:   m.webSearch.DeepSeekModel,
		WebSearchProxy:        m.webSearch.Proxy,
		BashEnv:               proxyEnv(m.proxyAddr, m.proxyCACert), // Bash 子命令默认走代理+信任 CA
		WorkingDir:            mainDir,                              // 本任务工作目录 <workDir>/tasks/<taskID>
		ToolOutputDir:         cmdOutDir(mainDir),
		MaxTurns:              m.maxTurns,                             // 0 = unlimited (configurable in agent management)
		Compaction:            compactionConfig(m.compactionWindow()), // long chats stay within the window
		Todos:                 actool.NewTodoStore(),                  // 会话级临时待办（TodoWrite），纯规划用，退出即丢
		// 命中预算(步数)→ SDK 跑收尾:向用户输出一句进展总结。Prompt 与收尾轮数可后台编辑(默认 10 轮)。
		Settlement:   wrapupSettlement("mainagent", nil),
		NonStreaming: m.nonStreaming(), // 该 profile 选非流式时走 Provider.Complete
		MaxTokens:    m.maxTokens(),    // 0 = 不发上限,由服务端默认值决定
	}
	if m.tx != nil { // persist raw human↔AI conversation; one accumulating file per segment
		opts.Transcript = m.tx
		// Segment 0 keeps the legacy "exp%d-main" name so existing transcripts still
		// load; each new session (seg>=1) gets its own file for a clean context.
		opts.SessionID = fmt.Sprintf("exp%d-main", ts.ID())
		if mainSeg > 0 {
			opts.SessionID = fmt.Sprintf("exp%d-main-s%d", ts.ID(), mainSeg)
		}
	}
	// 实验功能:开启后由 noa 接管上下文压缩(归档集中在 <workDir>/noa/<SessionID> 下,持久)。
	// session id 与 transcript 同规则(分段感知),使归档与恢复对齐。
	noaSession := fmt.Sprintf("exp%d-main", ts.ID())
	if mainSeg > 0 {
		noaSession = fmt.Sprintf("exp%d-main-s%d", ts.ID(), mainSeg)
	}
	enableNoa(&opts, m.noaEnabledFn, m.workDir, noaSession, noaWarn(noaSession))
	ctx = attachSideCapture(ctx, &opts)
	s := agentcore.NewSession(opts)
	defer s.Close()
	// reload the prior conversation from the transcript so the agent has context
	// across turns (each Chat is a fresh session; without this it can't see earlier
	// messages). First turn: no file yet → Resume loads nothing and proceeds.
	if m.tx != nil {
		_ = s.Resume(opts.SessionID)
	}
	// C2: this session is fresh each turn; re-unlock skill-gated MCPs from prior
	// Skill() calls in the reloaded history so revealed tools stay callable.
	seedUnlockFromHistory(s.Messages(), def.UnlockSkill)
	text, _, err := captureRunSession(ctx, s, message, func(r db.Activity) {
		if emit != nil {
			r.Worker = "mainagent"
			emit(r)
		}
	})
	return text, err
}
