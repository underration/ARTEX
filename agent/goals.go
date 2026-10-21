package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	acperm "github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// goalsDefaultTmpl is the built-in EDITABLE body (段 [A]) of the goals-decomposer
// prompt, seeded into agent_prompts. No template vars are used today.
const goalsDefaultTmpl = `あなたはペネトレーションテストの目標分解器です。あなたの役割はユーザー入力から**最終的に達成すべき結果**を識別することであり、攻撃手順を計画することではありません。

**ステップ 1（目標を分割する前にまず実行）：操作制約の抽出**
「タスク目標 / タスク説明」から、オペレーターによる【何をしてよく、何をしてはならないか】の明確な規定を識別し、set_constraints を呼び出して 1 件ずつ登録してください（説明や目標に操作制約が含まれない場合は抽出不要です）：
- type=deny：禁止された操作（「ポートスキャン禁止」「本番環境への書き込み/削除操作禁止」「ブルートフォース禁止」「特定サブドメインへの接触禁止」など）。
- type=allow：明示的に許可/限定された操作範囲（「パッシブ偵察のみ許可」「特定ドメインのみ対象」など）。
- 制約 ≠ 目標であり、≠ 攻撃手順でもあります：これは操作行動の境界に関する規定です。
- **制約は【自己完結しており、具体的なターゲットが明記されている】必要があります**：「現在のターゲット/現在のポート/現在の IP/現在のドメイン/当サイト」といった**指示語**は、タスク目標/説明にある**具体的な値**に置き換えてください。制約は実行フェーズのプロンプトに単独で注入されるため、文脈から切り離されると指示語が何を指すか判断できなくなります。
  例：ターゲットが https://abc.example.net の場合 →「現在のターゲットのみテスト許可」ではなく「abc.example.net のみテスト許可」と書く。「現在のポートのみテスト」ではなく「ターゲットポート 443 のみテストし、他のポートはスキャンしない」と書く。原文に「現在のターゲット」としか書かれていなくても、ターゲットアドレスが明確であればそのアドレスを埋め込んでください。
- **目標/説明に【明記または強調されている】制約のみを登録し、推測による捏造は厳禁です**。種別に迷った場合は deny（より保守的）を使用してください。
- 目標/説明に操作制約がまったくない場合は、set_constraints を**呼び出さないでください**。
制約の登録（ある場合）が完了してから、以下の目標分割に進んでください。

**目標 = 最終的に納品可能・検証可能な結果**

**目標ではないもの（サブ目標として列挙することを禁止）**：
- 情報収集、偵察、エンドポイントスキャン
- 脆弱性分析および検証プロセス
- 攻撃手順、エクスプロイト手段
- 結果の検証手順

**分割の原則**：
- ユーザーが記述した最終目標が 1 つだけの場合 → 1 つだけ出力する
- **互いに独立した**複数の最終成果物が存在する場合 → 個別に列挙する
- 明確な脆弱性クラスに対応付けられる場合は vulnclass を記載し、情報収集/ビジネスロジック系の目標は空欄にする
- ユーザーが言及していない目標を勝手に捏造することは厳禁

set_goals を呼び出して結果を送信してください。`

// goalsScopeTail is the code-owned tail appended after the editable goals body
// WHEN an asset store + task context are available. It teaches the decomposer to
// also lift the explicit asset scope out of the goal/description and register it
// via add_task_scope. Kept in code (not the DB-editable body) so it always applies
// on released DBs and can't be edited away — same pattern as the trafficTool tail.
const goalsScopeTail = `

**追加の役割：テスト対象アセット範囲の登録**
目標の分割に加えて、「タスク目標 / タスク説明」から**明示的に指定されたテスト対象アセット範囲**を識別し、add_task_scope を呼び出して登録してください（本タスクの認可境界であり、アセットテストカバレッジの分母にもなります）。**最小範囲の原則：ユーザーが明示したそのターゲットのみを登録し、勝手に範囲を拡大してはなりません。**
- ターゲットが URL またはホスト名付きのアドレス（https://xxx.example.com/path、app.example.com など）の場合 → その**完全なホスト名**を取得し、kind=subdomain、value=完全なホスト名 とします。
  例：ターゲットが https://a1b2c3.lab.example.net/path → kind=subdomain、value=a1b2c3.lab.example.net（example.net **ではありません**）。
  サブドメイン付きのホスト名をルートドメインに縮小することは**厳禁**です——xxx.example.com を見て example.com 全体を登録すると、ユーザーのターゲット外まで範囲が広がり、最小範囲の原則に違反します。
- ユーザーが与えたものが**サブドメインを一切含まない裸のルートドメイン**（直接 example.com と書かれているなど）である場合、または「サイト全体 / 全サブドメイン / 全ドメイン」と明示している場合に限り → kind=root_domain、value=example.com を使用します。
- 純粋な IP またはネットワーク帯域 → kind=ip / cidr、value=IP または CIDR。
- 企業範囲 (company) は**登録しないでください**——タスク作成直後はアセットシステム内にまだ該当企業が存在しないことが多く登録できないため、企業レベルの範囲は後続の plan フェーズに委ねます。
その他のルール：
- **目標/説明に明記されている**範囲のみを登録し、言及されていないドメイン/IP を推測・捏造することは厳禁です。
- reason には監査しやすいよう、どの文言に基づくかを簡潔に記述してください。
- 目標/説明に明確なアセット範囲が一切ない場合は、add_task_scope を**呼び出さないでください**。
まず add_task_scope で範囲を登録（ある場合）してから、set_goals を呼び出して目標を送信してください。`

// GoalSpec is one decomposed objective.
type GoalSpec struct {
	Text      string `json:"text"`
	VulnClass string `json:"vulnclass,omitempty"`
}

// DecomposeGoals asks the LLM to break a pentest task goal into discrete,
// independently-verifiable objectives (each becomes a goal node). Returns nil if
// no provider is configured or the call yields nothing — the caller then falls
// back to a rule-based split so goal nodes always exist.
//
// prov is supplied by the caller (rather than built here from a Config) so goal
// decomposition rides the SAME provider instance as the rest of the engine — it
// shares the rate limiter, gets recorded by llmrec, and participates in LLM
// failover instead of quietly bypassing all three.
//
// desc is the task's free-text description (背景：靶标范围/flag 数量/交战说明等).
// It is fed alongside the goal so the decomposer no longer splits blind — the
// prompt still forbids inventing anything the two texts don't state.
//
// emit, when non-nil, receives every LLM step (thinking/tool_use/result) with
// Worker="planner" so the round-0 goal-decomposition activity is visible in the UI.
//
// as + taskID, when non-nil/positive, wire the add_task_scope tool so the
// decomposer can register the explicit asset scope it extracts from the goal.
//
// ts is the task's exploration store: set_goals writes the decomposed goal nodes
// straight into it (the same managed tool the main agent uses to add goals at
// runtime). The returned specs are read back from the store so callers can emit
// per-goal activity and detect the "LLM produced nothing" case for their fallback.
func DecomposeGoals(ctx context.Context, prov llm.Provider, dataDir, goalText, desc string, as *db.AssetStore, ts *db.ExplorationStore, taskID int64, emit func(db.Activity)) []GoalSpec {
	if prov == nil {
		return nil
	}
	return DecomposeGoalsWithProvider(ctx, prov, dataDir, goalText, desc, as, ts, taskID, false, 0, emit)
}

// DecomposeGoalsWithProvider is the task-runtime variant used when a task has an
// ordered provider chain. It preserves the same tools and write behavior while
// letting the caller own provider selection/failover. maxTokens is the profile's
// per-reply output cap (0 = send none).
func DecomposeGoalsWithProvider(ctx context.Context, prov llm.Provider, dataDir, goalText, desc string, as *db.AssetStore, ts *db.ExplorationStore, taskID int64, nonStreaming bool, maxTokens int, emit func(db.Activity)) []GoalSpec {
	if prov == nil {
		return nil
	}
	// 目标拆解是一次性调用：不挂 transcript store，所以 agentcore 不会往 ctx 上挂
	// session id（它只在有 writer 时才挂，见 agentcore.Prompt）。而按 session-id 头
	// 做提示缓存/粘性路由的网关（opencode zen 缺 x-opencode-session 直接 400
	// MissingSessionID）读的就是 ctx 上这个值——不补就是「对话正常、拆解 400」。
	// 显式挂一个稳定 id：同一探索的拆解请求共享它（利于命中缓存），且命名与
	// planner/worker 不冲突，能被 llmrec.parseSession 正确归因。
	if ts != nil {
		ctx = transcript.WithSessionID(ctx, fmt.Sprintf("exp%d-goals", ts.ID()))
	}
	// worker="goals" tags the goal nodes' provenance; ts/taskID let set_goals link
	// each goal under the task root. This is the catalog's real set_goals tool, so a
	// web-edited description/schema on it applies here too.
	tsx := &ToolSet{as: as, ts: ts, taskID: taskID, worker: "goals"}
	// Description rides in the user message (same channel as the goal), NOT via the
	// {{.EngagementDescription}} template var — else a prompt that references the var
	// would inject the description twice. System prompt stays pure static instructions.
	sys := renderSystem("goals", goalsDefaultTmpl, GoalsVars{DataDir: dataDir, Now: nowStr()})
	// set_constraints 始终可用(不依赖 asset store):正文已含「先抽操作约束再拆目标」这步
	// (可在 agent 编辑页改措辞),这里只需接上工具。
	tools := []actool.CoreTool{tsx.setGoals(), tsx.setConstraints()}
	// Wire add_task_scope only when we have a real asset store + task to write to.
	// The scope-extraction tail is appended in lockstep so the prompt never asks for
	// a tool that isn't present.
	if as != nil && taskID > 0 {
		tools = append(tools, tsx.addTaskScope())
		sys += goalsScopeTail
	}
	userMsg := "タスク目標：\n" + goalText
	if d := strings.TrimSpace(desc); d != "" {
		userMsg += "\n\nタスク説明（背景情報、ターゲット範囲/flag 数/エンゲージメント説明を含む場合があります。参考情報として扱い、言及されていない内容を推測・捏造しないでください）：\n" + d
	}
	// Use captureRun so every LLM step is emitted as an activity record (visible in
	// the plan tab under the round-0 marker). Falls back gracefully when emit is nil.
	captureEmit := func(r db.Activity) {
		if emit != nil {
			r.Worker = "planner"
			emit(r)
		}
	}
	captureRun(ctx, agentcore.Options{
		Provider:               prov,
		SystemPrompt:           []string{sys},
		Tools:                  tools,
		PermissionMode:         acperm.ModeBypass,
		DisableBackgroundTasks: true,
		// 3 步(抽约束 → 登记范围 → 拆目标)各需一次工具调用,给足回合避免收尾前漏调 set_goals。
		MaxTurns:     8,
		NonStreaming: nonStreaming, // 该 profile 选非流式时走 Provider.Complete
		MaxTokens:    maxTokens,    // 0 = 不发上限,由服务端默认值决定
	}, userMsg, captureEmit)
	// set_goals persisted the goals directly; read them back so the caller sees what
	// was written (empty slice ⇒ the LLM produced nothing ⇒ caller falls back).
	if ts == nil {
		return nil
	}
	nodes, _ := ts.ListByKind(db.KindGoal, 10000)
	var out []GoalSpec
	for _, n := range nodes {
		var p struct {
			Text      string `json:"text"`
			VulnClass string `json:"vulnclass"`
		}
		_ = json.Unmarshal(n.Payload, &p)
		if strings.TrimSpace(p.Text) != "" {
			out = append(out, GoalSpec{Text: p.Text, VulnClass: p.VulnClass})
		}
	}
	return out
}
