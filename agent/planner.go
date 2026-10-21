package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// Planner is the event-driven LLM planner (docs §4.3): each time the asset or
// exploration graph changes (debounced), it reads the exploration route, queries
// assets, judges whether the task goal is met, and emits 0..N exploration intents
// into the frontier. It is the sole intent generator.
type Planner struct {
	findingRecorder   FindingRecorder
	prov              llm.Provider
	model             string
	tx                *transcript.Store                      // raw LLM conversation persistence (nil = off)
	window            int                                    // context window in tokens (for compaction)
	windowFn          func() int                             // optional dynamic task-chain minimum
	maxTurns          int                                    // max agent turns per run (0 = unlimited)
	killWork          func(intentID int64) error             // engine callback to terminate a running work (nil = off)
	steerWork         func(intentID int64, msg string) error // engine callback to steer a running work mid-run (nil = off)
	proxyAddr         string                                 // recording proxy for WebFetch (empty = direct)
	proxyCACert       string                                 // recording proxy's CA cert path (HTTPS verify)
	webSearch         WebSearchOpts                          // web_search tool backend selection (off by default)
	workDir           string                                 // shared work dir (surfaced in prompt as artifact-output target)
	injectConstraints func() bool                            // resolver: inject task operation constraints into system prompt? (nil = yes)
	nonStreamingFn    func() bool                            // resolver: use non-streaming (Complete) path? (nil = streaming)
	noaEnabledFn      func() bool                            // resolver: use experimental noa compaction? (nil = off)
	maxTokensFn       func() int                             // resolver: per-reply output cap (nil/0 = send no cap)
	compactor         *Compactor                             // cold-node compaction (§7); nil = disabled

	// todos keeps ONE plan-scratchpad per task (keyed by exploration id) so the
	// planner's multi-step plan survives across wake-ups — each Plan() is a fresh
	// session, but the shared store lets it record a serial exploit chain once and
	// dispatch it step-by-step over rounds instead of front-loading it in parallel.
	todoMu sync.Mutex
	todos  map[int64]*actool.TodoStore
}

func NewPlanner(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int) *Planner {
	return &Planner{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns, todos: map[int64]*actool.TodoStore{}}
}

func (p *Planner) SetCompactionWindowResolver(fn func() int) { p.windowFn = fn }

// SetCompactor wires the cold-node compactor (cold-digest §7). Called each
// planner wake-up to advance the round counter, maintain cold stamps, and
// (off the hot path) fold cold nodes into digests. nil = feature disabled.
func (p *Planner) SetCompactor(c *Compactor) { p.compactor = c }

// SetNonStreaming wires a resolver deciding whether runs use the non-streaming
// model path (true = non-streaming). nil/unset = streaming (default).
func (p *Planner) SetNonStreaming(fn func() bool) { p.nonStreamingFn = fn }

func (p *Planner) nonStreaming() bool { return p.nonStreamingFn != nil && p.nonStreamingFn() }

// SetNoaEnabled wires a resolver deciding whether runs use the experimental noa
// context-compression mechanism. nil/unset = off (built-in compaction). Read per
// run so the settings toggle takes effect without rebuilding the agent.
func (p *Planner) SetNoaEnabled(fn func() bool) { p.noaEnabledFn = fn }

// SetMaxTokens wires a resolver for the per-reply output cap. nil/unset or 0 =
// send no cap and let the endpoint decide. Read per run, like nonStreaming.
func (p *Planner) SetMaxTokens(fn func() int) { p.maxTokensFn = fn }

func (p *Planner) maxTokens() int {
	if p.maxTokensFn == nil {
		return 0
	}
	return p.maxTokensFn()
}

func (p *Planner) compactionWindow() int {
	if p.windowFn != nil {
		return p.windowFn()
	}
	return p.window
}

// SetProxy points the planner's WebFetch at the recording proxy plus the CA cert
// it trusts to verify HTTPS through it (empty addr = direct).
func (p *Planner) SetProxy(addr, caCert string) { p.proxyAddr, p.proxyCACert = addr, caCert }

// SetWebSearch selects the web_search backend for the planner (off by default).
func (p *Planner) SetWebSearch(o WebSearchOpts) { p.webSearch = o }

// SetConstraintInject wires a resolver deciding whether this task's operation
// constraints get injected into the planner system prompt. Read per round so the
// settings toggle takes effect without rebuilding the agent. nil = inject (default).
func (p *Planner) SetConstraintInject(fn func() bool) { p.injectConstraints = fn }

// wantConstraints reports whether constraint injection is enabled (default yes).
func (p *Planner) wantConstraints() bool { return p.injectConstraints == nil || p.injectConstraints() }

// todoFor returns the task's persistent planning todo store, creating it on first
// use. Shared across all of this task's planner wake-ups.
func (p *Planner) todoFor(expID int64) *actool.TodoStore {
	p.todoMu.Lock()
	defer p.todoMu.Unlock()
	s := p.todos[expID]
	if s == nil {
		s = actool.NewTodoStore()
		p.todos[expID] = s
	}
	return s
}

// SetKillWork wires the engine's per-work terminate callback so the planner's
// kill_work tool can stop a single running worker.
func (p *Planner) SetKillWork(fn func(intentID int64) error) { p.killWork = fn }

// SetSteerWork wires the engine's per-work steering callback so the planner's
// steer_work tool can inject a mid-run course-correction into a running worker.
func (p *Planner) SetSteerWork(fn func(intentID int64, msg string) error) { p.steerWork = fn }

// renderPlannerTodos formats the persistent planning todo for injection into the
// wake-up prompt (empty when there are no todos yet — first wake-up).
func renderPlannerTodos(items []actool.Todo) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【あなたの計画 ToDo（ウェイクアップ間で保持、前回のラウンドであなたが記録したもの）】：\n")
	for _, it := range items {
		mark := map[actool.TodoStatus]string{actool.TodoPending: "☐", actool.TodoInProgress: "▶", actool.TodoCompleted: "✔"}[it.Status]
		if mark == "" {
			mark = "☐"
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", mark, it.Content))
	}
	b.WriteString("これに基づいて進行してください：【前提ステップが完了している / 依存する fact がすでに存在する】次のステップに対してのみインテントを発行し、TodoWrite でリストを更新してください（すでに fact で満たされたステップは completed にマーク）。リスト内でまだ pending/in_progress になっているステップを重複して発行しないでください。")
	return b.String()
}

// TriggerEvent describes what concretely caused this planning round to fire, so
// the planner looks first at the actual change instead of re-scanning the whole
// overview. Kind:
//
//	"done"    — a worker finished intent IntentID (its output conclusion is fetched).
//	"finding" — a worker reported a finding on intent IntentID (Detail = 摘要).
//	"goal"    — the human (via 主 agent 的 set_goals) added one OR MORE goals in a
//	            single call (Goals = 本次新增的目标文本，1+ 条；set_goals 支持批量).
//	"goal_deleted" — the human deleted a goal from 总览的目标管理 (Detail = 被删目标文本).
//	"goal_edited"  — the human edited a goal from 总览的目标管理 (OldGoal→NewGoal 文本).
//	"cancelled" — the human deleted intent IntentID (Detail = 删除原因). The intent is
//	            stopped (not deleted) and the reason is attached to it as a fact.
type TriggerEvent struct {
	Kind     string
	IntentID int64
	Detail   string
	Summary  string   // Kind=="cancelled" 专用：删除前捕获的意图摘要（真删除后节点已不存在，无法再查）
	Goals    []string // Kind=="goal" 专用：本次 set_goals 新增的目标文本（1 条或多条）
	OldGoal  string   // Kind=="goal_edited" 专用：修改前的目标文本
	NewGoal  string   // Kind=="goal_edited" 专用：修改后的目标文本
	Hints    []string // Kind=="hint" 专用：本次 add_hint 新增的提示文本（1 条或多条）
}

// renderTriggers spells out the change(s) that fired this round: for a finished
// worker — which intent + its output conclusion; for a finding — which intent +
// what was found. Empty for time/heartbeat wakes. Reads the store (best-effort;
// a blank field never blocks the round).
func renderTriggers(ts *db.ExplorationStore, evs []TriggerEvent) string {
	if len(evs) == 0 || ts == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【今回のラウンドをトリガーした実際の変動（まずここを確認してから、方向性を追加するか決定してください）】：")
	for _, ev := range evs {
		switch ev.Kind {
		case "goal":
			if len(ev.Goals) == 1 {
				b.WriteString(fmt.Sprintf("\n- 人間（メイン agent）が新しい目標を追加しました：%s —— 達成すべき新しい目標です。対応するインテントがまだない場合は、これに基づいて探索方向を補足してください。", ev.Goals[0]))
			} else {
				b.WriteString(fmt.Sprintf("\n- 人間（メイン agent）が %d 個の目標を追加しました：%s —— いずれも達成すべき新しい目標です。対応するインテントがまだない目標について、1 つずつ探索方向を補足してください。", len(ev.Goals), strings.Join(ev.Goals, "；")))
			}
		case "hint":
			if len(ev.Hints) == 1 {
				b.WriteString(fmt.Sprintf("\n- 人間（メイン agent）が戦略ヒントを 1 件追加しました：%s —— 探索グラフに登録されました。対応するインテントがまだない場合は、これに基づいて探索方向を調整・補足してください。", ev.Hints[0]))
			} else {
				b.WriteString(fmt.Sprintf("\n- 人間（メイン agent）が %d 件の戦略ヒントを追加しました：%s —— いずれも探索グラフに登録されました。1 件ずつこれに基づいて探索方向を調整・補足してください。", len(ev.Hints), strings.Join(ev.Hints, "；")))
			}
		case "goal_deleted":
			b.WriteString(fmt.Sprintf("\n- 人間がこの目標を削除しました：%s —— この目標は削除されました。残りの目標・方向性を再判定してください（この目標のためにインテントを発行する必要はもうありません）。", ev.Detail))
		case "goal_edited":
			b.WriteString(fmt.Sprintf("\n- 人間が目標を「%s」から「%s」に変更しました —— 新しい目標に基づいて探索方向を調整してください（元の方向性が不要になった場合は発行を停止してください）。", ev.OldGoal, ev.NewGoal))
		case "finding":
			b.WriteString(fmt.Sprintf("\n- インテント #%d（%s）の worker が finding を報告しました：%s", ev.IntentID, intentSummary(ts, ev.IntentID), ev.Detail))
		case "cancelled":
			// 意图内容优先用删除时捕获的 Summary（真删除后节点已不存在，intentSummary 查不到）。
			sm := ev.Summary
			if sm == "" {
				sm = intentSummary(ts, ev.IntentID)
			}
			b.WriteString(fmt.Sprintf("\n- インテント #%d がユーザーによって削除されました（インテント内容：%s、削除理由：%s）。このインテントは削除され実行されません。これに基づいて再計画してください。", ev.IntentID, sm, ev.Detail))
		default: // "done"
			b.WriteString(fmt.Sprintf("\n- インテント #%d（%s）の worker が終了し、結論を出力しました：%s", ev.IntentID, intentSummary(ts, ev.IntentID), workerOutput(ts, ev.IntentID)))
			if fids := factIDsYielded(ts, ev.IntentID); fids != "" {
				b.WriteString(fmt.Sprintf("；本インテントが新たに生成したファクト id：%s ", fids))
			}
		}
	}
	b.WriteString("\n（完全な詳細は必要に応じて node_detail / get_worker_output / list_findings で確認できます。）")
	return b.String()
}

// factIDsYielded lists the fact ids an intent produced this run as "#12、#15", so the
// planner can jump straight to the round's incremental facts. Empty (best-effort) when
// the intent yielded no facts or the lookup fails.
func factIDsYielded(ts *db.ExplorationStore, id int64) string {
	ids, err := ts.FactsYielded(id)
	if err != nil || len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, fid := range ids {
		parts[i] = fmt.Sprintf("#%d", fid)
	}
	return strings.Join(parts, "、")
}

// intentSummary reads an intent node's one-line summary (best-effort, "?" on miss).
func intentSummary(ts *db.ExplorationStore, id int64) string {
	n, err := ts.GetNode(id)
	if err != nil || n == nil {
		return "?"
	}
	var p map[string]any
	if json.Unmarshal(n.Payload, &p) == nil {
		if s, ok := p["summary"].(string); ok && s != "" {
			return s
		}
	}
	return "?"
}

// workerOutput returns the finished worker's conclusion for an intent — the last
// 'result' (else 'text') activity's full detail, truncated. Same source get_worker_output uses.
func workerOutput(ts *db.ExplorationStore, id int64) string {
	acts, _, err := ts.ActivityList(&id, 0, 1000)
	if err != nil {
		return "(出力の取得に失敗)"
	}
	var pick *db.Activity
	for i := range acts {
		if acts[i].Kind == "result" {
			pick = &acts[i]
		} else if acts[i].Kind == "text" && pick == nil {
			pick = &acts[i]
		}
	}
	if pick == nil {
		return "(この work にはまだ出力記録がありません)"
	}
	out, _ := ts.ActivityDetail(pick.ID)
	if out == "" {
		out = pick.Summary
	}
	return truncOutput(out, 800)
}

// truncOutput caps a worker-output blob so the trigger context doesn't bloat the
// system prompt every round; full text is one get_worker_output call away.
func truncOutput(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + " …（切り詰められました。完全な内容は get_worker_output を参照してください）"
}

// renderGraphOverview folds the pre-computed graph_overview snapshot into the
// wake-up prompt so the planner starts each round with the full situation in
// hand — saving the round-trip it would otherwise spend calling the tool. It is
// the exact same JSON graph_overview would return; deeper detail is still one
// tool call away (node_detail / list_facts / …).
func renderGraphOverview(data map[string]any) string {
	b, err := json.Marshal(data)
	if err != nil {
		return "" // fall back to the model calling graph_overview itself
	}
	return "\n\n【今回のラウンドの状況（graph_overview の事前取得であり、同ツールを呼び出した結果と同等です。詳細が必要な場合は必要に応じて node_detail/list_facts などを呼び出してください）】：\n" + string(b)
}

// plannerDefaultTmpl is the built-in EDITABLE body (段 [A]) of the planner prompt,
// seeded into agent_prompts. Goal is a {{.Goal}} template var; the 中间产物输出规约
// tail is code-owned (artifactSpec) and appended by plannerSystem after rendering.
const plannerDefaultTmpl = `あなたはサイバーセキュリティプラットフォームの承認済みペネトレーションテストシステムにおける「プランナー」であり、頻繁にウェイクアップされます（グラフが変化するたびに起動されます）。役割：状況の把握 → 目標の判定 → **まだカバーされていない新しい方向性が確実に存在する場合にのみ**探索インテントを補足すること。あなたはプランナーであり、実行者ではありません：今回のラウンドの成果物はすべて【インテントの生成/明確化】または【目標の判定】のみでなければならず、plan の中で実作業を行ってはなりません。

タスク目標：{{.Goal}}

**今回のラウンドでいくつのインテントを生成すべきか（まずこれを明確に考えること）**：
- **絶対条件（最優先）**：【目標が未達成】であり、かつ【現在 open または running のインテントが 1 つもない】（frontier_open=0 かつ running_intents が空）場合、今回のラウンドでは目標達成に向けて前進するインテントを【必ず】最低 1 つ生成しなければなりません——実行中で結果を待つべき work もなく、キューに入っている方向性もない状態で 0 インテントを出力することはタスクの停止を意味します。たとえ既知の方向性がすべて recent_done にあったとしても、以下の done/exhausted/blocked の判断基準に従って別ルートを開くか継続インテントを発行してください。
- 絶対条件以外では、**0 個のインテントを出力することは正常な結果ですが、正当な理由が必要です**（「少なく発行する方が無難」というデフォルトではありません）：①**カバー済み**——思いついた方向性がすべて現在 open/running のインテントによって処理されている（言い回しを変えて既存のインテントを重複生成するのは重大な誤りです）；②**依存待ち**——次のステップが現在実行中の work の成果物に依存しており、それがまだ出ていない（この時点で無理に発行すると下流が前提条件を得られず空回りするため、次回のウェイクアップでグラフが更新されるのを待ってから発行すべきです）。
- 逆に言えば：【未カバーであり、かつ実行中の work に依存しない】新しい方向性が確実に存在する場合、または目標が未達成でスコープ内にまだ未テストの攻撃面が残っている場合は、躊躇せず発行してください——0 インテントを怠慢のデフォルトにしてはなりません。

**各ウェイクアップ時の意思決定フロー**：

1. **完全な状況は本プロンプトの下部に添付されています**（graph_overview の返却値そのものであるため、再度呼び出す必要はありません）：task（元のタイトル＋目標/ルートノード）、アセット集計、goals＋ステータス、open/running/recent_done インテント、sites_without_endpoints（エンドポイントのないサイト、未探索の可能性がある方向性を示唆）、facts（探索ファクト数、脆弱性とは別カテゴリ）、recent_facts（{id,summary,confidence?}）。
   - **スコープ**：探索ノード（goals/インテント/facts/findings）には本タスクのものだけが含まれます。**アセットグラフはグローバル共有**です（複数タスクで同一のものを共有しており、アセット集計はスコープ内のグローバルな数値であり本タスク固有ではありません）——本タスクと無関係なアセットが現れた場合は無視してください。
   - **系譜（リネージ）**：各インテントには parents（上流：どのファクト/インテントから派生したか）と yields（下流：どのファクト/検出結果を生成したか）が付与されており、recent_facts の各項目には from_intent が付与されています。これに基づき「どのファクトがどの方向性から得られたか、それらを統合して新しい方向性を導き出せるか」を理解してください。
   - **否定的/疑問の残る観察**（recent_facts 内の「ポート閉鎖/インジェクション不可」など）は worker の観察結果であり、確定事項ではありません：採用する前にまず node_detail(id) で evidence を確認してください——evidence が確固としており、confidence=observed かつ手段を尽くしている場合のみ、その方向性は一旦塞がれたと見なします。evidence が欠けている、「〜のように見える/1 回試しただけ」、または confidence=inferred の場合は【未解明】として扱い、スコープ内であり他のインテントでカバーされていなければ、裏付けまたは覆すための再確認インテントをデフォルトで 1 件発行してください（**同一の否定方向に対する再確認は最大 1 回まで**。再確認後も否定であり証拠が合理的であれば、その結論を尊重しそれ以上は発行しません）。
   - **より深い詳細が必要な場合のみ必要に応じて呼び出す**：list_facts（ページネーション、最新順、デフォルト 20、q でフィルタ可能、before でページ送り、total/has_more 付き）、list_findings（全脆弱性）、node_detail(id)（完全な証拠/詳細。一覧や recent_facts は要約のみ）、list_assets（pull：q 検索、type/company_id/task_id フィルタ、ページネーション、または id/ids で直接取得）、asset_neighbors。アセットはグローバル共有のため、デフォルトで全件取得しないでください。

2. **目標の判定（中核的な役割）**：goals フィールドには目標とステータスが含まれています。ある検出結果/ファクトによって証明された未達成の目標に対しては、prove_goal(goal_id, evidence_id, reason) を呼び出して met にマークしてください。**あなたがマークしたものが最後の未達成目標であった場合、システムは自動的にタスク全体の完了を判定します**——完了処理は個々の prove_goal によってのみ駆動され、他に「ワンクリック完了」のような手段はありません。
   - ⚠️ **定量的検収の照合（早まった完了認定は厳禁）**：目標に定量的な条件（カバレッジ X% 達成、flag を N 個取得、特定権限の獲得など）が含まれる場合、prove_goal の前に【必ず】上部の graph_overview の実測値（coverage.pct、findings_total カウントなど）と照合してください。基準に達していない場合は prove_goal を【禁止】し、不足分を補うインテントを発行してください。「概ね達成した/核心部分は落とした」という理由で前倒しに met をマークしてはなりません。例：カバレッジ 100% が求められているのに実測 coverage.pct=40% の場合 → 未達成であり、追加テストのインテントを引き続き発行します。

3. **（任意、序盤のみ、極めて軽量な）プローブによる理解**：グラフ内にまだほとんど fact がなく（recent_facts がほぼ空でタスク開始直後）、状況だけでは初期インテントを具体的に記述できない場合に限り、Bash などを用いてターゲットに対しごく少量の読み取り専用プローブ（1〜2 回の curl でトップページ/フィンガープリントを見るなど）を行うことができます。**唯一の正当な成果物は、より的確な 1 文のインテント記述です**——脆弱性の発見/検証/悪用では決してなく、エンドポイント/ディレクトリ/パラメータの列挙結果でもありません（それらは worker の仕事であり、インテントとして記述して発行してください）。3 つの厳格な境界：
   - グラフ内にすでに worker が生成した fact がある場合（facts>0 / recent_facts が非空）→ 自分でプローブすることは【禁止】です。すべての判断は既存の fact に基づいて行い、今回のラウンドの成果物は「新しいインテントの発行」または「終了」のみとします。特定の手がかりを深掘りしたい場合 → 自分で curl するのではなく、worker に調査させるインテントを発行してください。
   - たとえ序盤であっても最大 3 回以下のプローブで切り上げ、初期インテントを明確にすることだけを目的にしてください。「素早い方向付け」ではなく「深い調査検証」（エンドポイント/ディレクトリの逐一列挙、id の逐一試行、デコードチェーン、同一 API の反復プローブ、あらゆるインジェクション/権限昇格/脆弱性のテスト検証——これらはすべて worker の本格的な作業です）をしていることに気づいたら、即座に手を止めてインテントとして書き出してください。
   - 既存のファクトや状況から判断できる場合は、そもそもプローブを行う必要はありません。

4. **どの新しい方向性を補足するか決定する**：**ここでの「抑制」とは【既存のインテントを重複させないこと】のみを指し、「なるべく少なく発行する」という意味ではありません**——目標が未達成である間、デフォルトの問いかけは「目標に迫るために、まだカバーされていないより深く、より鋭い攻め手は他にないか」であり、「そろそろ終了してよいか」ではありません。インテントは【オープンな探索方向】（固定された種別やメニューではありません）であり、既知のファクト、アセット、目標を組み合わせて方向性を判断し、open + running + recent_done と 1 つずつ照合します：
   - すでに open/running でカバーされている → 生成しない（処理中）。
   - recent_done に出現している → **まずそのインテントの state（各項目に付与）を見てどのように停止したかを判別してから決定する**：
     · **done（正常完了）**：カバー済み → そのまま再発行はしない。行き詰まりかどうかは state ではなく yields された fact の結論を見る。【実質的な新しいメカニズム】（新しいファクト/アセット/パラメータ/明確に異なる攻め手）が現れた場合にのみ再発行し、かつ summary に前回との違いを明記する。言い回しの変更や「もう一度試せばうまくいくかもしれない」は認められず、再試行は禁止。
     · **exhausted（予算使い切り、探索の途中で打ち切られ一部のみ書き戻された）/ blocked（モデルまたはネットワーク障害により、実質的に探索できていない）**：どちらも途中で完了しておらず情報が不完全——まず get_worker_trace / get_worker_output で実際にどこまで進み、どこで詰まったかを確認してから、次の中から選択する：突破寸前で予算により打ち切られた →「前回の進捗を引き継いで継続する」インテントを発行；純粋な外部障害で実行できなかった（blocked に多い）→ 同じ方向性をそのまま再発行；毎回同じ箇所で詰まる → 攻め手/方向性を変える。根拠は常に trace 内の実際の進捗であり、state そのものではありません。
   - いかなるインテントでも全くカバーされていない新しい方向性 → 生成する。
   - すべての既知の方向性が現在 open/running のインテントでカバーされている → 生成せずそのまま終了する（実行中/キュー待ちの work があるため、それらの進行を待つ）。ただし recent_done でのカバーのみが残り、open/running がなく目標も未達成の場合 → 冒頭の絶対条件に従い、必ず別ルートを開くか継続インテントを発行する。
   - **カバレッジより深さを優先する**：coverage は下限/検収項目であり、探索目標そのものではありません。高価値な侵入口（RCE/権限昇格/データ流出につながる可能性があるもの）を発見した後は、カバレッジを均すために広く浅く各アセットをテストするのではなく、そのルートを【奥深くまで突き破る】インテントを優先して発行してください。
   - **ルートの多様性を保ち、早すぎる収束を避ける**：目標が未達成のとき、既存のインテントがすべて同一のルート/侵入口に集中しており、【本質的に異なる】未カバーの方向性（別の侵入口/別種のアセット/別のエクスプロイトチェーン）が存在する場合は、同一路線上に同義のインテントを追加するのではなく、その分岐方向を優先して補足してください（文言ではなく実質的な差異を見ます。その分岐方向がすでに既存のインテントでカバーされている場合は生成しません）。理想はメカニズムの異なる 2〜3 本のルートを並行させることであり（「アップロードチェーンからの攻略」と「認証バイパスからの攻略」など）、いずれかのルートが【目標に迫る】証拠を出してから初めてリソースをそこに集中させます。**ただし多様性は常に冒頭の【操作制約】に従います**：制約によって除外された侵入口/ポート/ホスト/操作については、たとえ本質的に異なっていても絶対にインテントを生成してはなりません。

   **直列エクスプロイトチェーン：分割して並列発行せず、段階的に発行せよ。** 強い依存関係を持つ直列チェーン（①→②→③、後のステップが前のステップの実際の成果物に依存するもの）：一度に並列発行してはなりません（下流はまだ存在しない前提条件を得られず、重複作業や空回りをするだけです）。TodoWrite を使ってチェーン全体を ToDo として記録し（1 ステップ 1 項目）、今回のラウンドでは「前提条件が満たされている」ステップ（通常は最初のステップ）のみを発行します。それが fact を生成した後、次回のウェイクアップ時（プロンプトに ToDo リストが付与されます）に次のステップを発行し、満たされたステップを completed にマークしてください。「同じ 1 つの作業」を 2 つに分割しないでください（「トリガー箇所の確認」と「トリガー箇所の発火」は同一ステップです）。【並行しており互いに依存しない】軸（関連のない複数のエンドポイントの列挙など）に対してのみ、複数インテントの並列発行を使用してください。

5. **送信**：【1 回の】add_intent 呼び出しで、絞り込んだ新しい方向性を一括送信します（intents 配列、最も価値の高いものを最大 4 件まで。1 件ずつ複数回呼び出さないでください）：
   - **summary**：その方向性を 1 文の自然言語で記述します（テスト対象アセットの完全なアドレス + 何をするか + なぜか）。固定の分類には当てはめません。重複排除は主にこれと既存インテントとの照合によって行われます。
   - **asset_ids**：この方向性でテスト/攻撃対象とするアセット id（可能な限り指定、0/1/複数、list_assets から取得）——具体的なアセット（サイト/API/パラメータ/ホスト）をめぐる方向性である限り必ず渡し、カバレッジの重複排除やアセットチェーンへの連結に使用します。複数アセットにまたがる場合はすべて渡し、具体的なアセットを持たない純粋なグローバル偵察の場合のみ空欄にします。
   - **parent_ids**：この方向性がどの上流ノードを統合して導き出されたか（任意、0/1/複数）——複数のファクトを組み合わせて 1 つのインテントが生まれた場合はすべて渡し、特定の上流インテント/検出結果から派生した場合もその id を渡し、トップレベルの全く新しい方向性の場合は空欄にします。

重複させず、無理に数を揃えないこと。しかし目標が未達成であり、まだカバーされていないより深い攻め手があるなら、迷わず発行すること。簡潔、集中的、かつ効率的に。`

func plannerSystem(goal, dataDir, workDir string) string {
	body := renderSystem("planner", plannerDefaultTmpl, PlannerVars{Goal: goal, DataDir: dataDir, Now: nowStr()})
	return body + artifactSpec(workDir)
}

// Plan runs one planning round. emit, if non-nil, receives the planner's execution
// steps (so users can see how it reads the situation and judges goals — the
// planner is the intent generator and was previously a black box). Returns whether
// the planner judged the goal met.
// triggers carries the concrete change(s) that fired this round — worker(s) done
// and/or finding(s) reported (may be several — the engine debounces a burst; empty
// for time/heartbeat wakes). They are spelled out at the top of the prompt so the
// planner looks first at the actual change (which intent, its output/finding).
func (p *Planner) Plan(ctx context.Context, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, goal string, triggers []TriggerEvent, emit func(db.Activity)) (met bool, reason string, err error) {
	// cold-digest §2.3/§7: advance this task's planner-round counter, maintain the
	// cold_since_round stamps, and (if a threshold is hit) kick off background
	// compaction. Synchronous part is cheap (a few queries); the LLM compaction
	// runs in a detached goroutine so it never adds latency to this round.
	p.compactor.OnPlannerRound(ctx, ts)
	tsx := NewToolSet(ts, "planner")
	tsx.SetFindingRecorder(p.findingRecorder)
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetTaskID(taskID)
	tsx.SetCoverageEnabled(as == nil || as.CoverageEnabled(taskID))
	tsx.killWork = p.killWork   // enable kill_work tool (nil = unavailable)
	tsx.steerWork = p.steerWork // enable steer_work tool (nil = unavailable)
	if origin, _ := ts.OriginFactID(); origin > 0 {
		tsx.SetOwnerNode(origin) // planner-side anchors default to the task root (origin fact)
	}
	// 领域工具 + 基础默认工具集（Read/Write/Edit/MultiEdit/LS/Glob/Grep/Bash）
	// 资产覆盖度功能关闭时剔除 add_task_scope/list_untested_assets（不入 prompt）。
	base := append(tsx.DropCoverageTools(tsx.PlannerTools()), actool.DefaultTools()...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts)})
	tools, def, cleanup := AugmentTools(ctx, "planner", base)
	defer cleanup()
	// 关键态势（刚完成的意图 + 预取的完整图）改放【本轮 user 输入】(见下方 input)，system
	// 只留静态规划正文。move-out 让 system 每轮稳定、更利于缓存；代价是若单轮变长，态势可能
	// 被 compaction 压缩（planner 单轮通常短，风险低）。situational 会拼进下方 input。
	situational := renderTriggers(ts, triggers) + renderGraphOverview(tsx.graphOverviewData())
	// 任务级 deadline / 终局模式(经 ctx 注入,见 taskclock.go)。终局那一轮把任务超时
	// planner 收尾词作为【本轮操作指令】拼进本轮 user 输入(随 situational),让它只做最后
	// 目标判定、不产新意图。
	tc := taskClockFrom(ctx)
	if tc.Final {
		situational += "\n\n【タスク終了時の締めくくり処理（本ラウンドの特別指令であり、上記の通常の計画フローに優先します）】：" + resolveTaskTimeoutWrapup("planner")
	}
	// 本任务的工作目录 <workDir>/tasks/<taskID>，先建好。
	taskDir := ensureRunDir(p.workDir, taskID, 0)
	ctx = intercept.WithReviewContext(ctx, taskDir, intercept.ReviewBackground{})
	sysBody := plannerSystem(goal, p.workDir, taskDir)
	if p.wantConstraints() {
		sysBody += constraintBlock(ts) // 操作约束(若有)注入系统提示,框定探索边界
	}
	system, boundary := deferredSystem(sysBody, def)
	// planner 无自身墙钟预算;有 deadline 时把 MaxDuration 夹逼到剩余,让在跑的规划轮在
	// 任务到点时进收尾(因超时→任务超时词,因步数→per-run 词)。
	maxDur, clamped := clampMaxDuration(tc.DeadlineUnix, 0)
	settle := wrapupSettlement("planner", nil)
	if tc.DeadlineUnix > 0 {
		settle = wrapupSettlementForTask("planner", nil, clamped)
	}
	opts := agentcore.Options{
		Provider:        p.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		EnableWebFetch:  true, // 走记录代理留痕；载入代理 CA 验证 MITM 重签的 HTTPS 证书
		WebFetchProxy:   p.proxyAddr,
		WebFetchCACert:  p.proxyCACert,
		// 联网搜索(可选)。ddgs 无需 key；brave-free 需 BraveKey；tavily 需 TavilyKey。
		// WebSearchProxy 是独立出口代理(http/https/socks5)，与记录流量的 MITM 代理无关；空则直连。
		EnableWebSearch:       p.webSearch.Enabled,
		WebSearchBackend:      p.webSearch.Backend,
		BraveSearchAPIKey:     p.webSearch.BraveKey,
		TavilySearchAPIKey:    p.webSearch.TavilyKey,
		DeepSeekSearchBaseURL: p.webSearch.DeepSeekBaseURL,
		DeepSeekSearchAPIKey:  p.webSearch.DeepSeekAPIKey,
		DeepSeekSearchModel:   p.webSearch.DeepSeekModel,
		WebSearchProxy:        p.webSearch.Proxy,
		BashEnv:               proxyEnv(p.proxyAddr, p.proxyCACert), // Bash 子命令默认走代理+信任 CA
		WorkingDir:            taskDir,                              // 本任务工作目录 <workDir>/tasks/<taskID>
		ToolOutputDir:         cmdOutDir(taskDir),
		MaxTurns:              p.maxTurns, // 0 = unlimited (configurable in agent management)
		MaxDuration:           maxDur,     // 0=不限;有 deadline 时=距 deadline 剩余
		Compaction:            compactionConfig(p.compactionWindow()),
		// 跨唤醒共享的规划待办：让串行链在多轮之间保留（session 是新的，store 不是）。
		Todos: p.todoFor(ts.ID()),
		// 命中【本轮】步数预算→ SDK 跑收尾:把本轮已想清楚的结论落地(该派的 add_intent、
		// 能证的 prove_goal、串行链记 TodoWrite),而非停止规划——planner 之后仍会被反复唤醒。
		// clamped(被任务 deadline 夹逼)时改用 PromptByReason(见 wrapupSettlementForTask)。
		Settlement:   settle,
		NonStreaming: p.nonStreaming(), // 该 profile 选非流式时走 Provider.Complete
		MaxTokens:    p.maxTokens(),    // 0 = 不发上限,由服务端默认值决定
	}
	if p.tx != nil { // persist raw LLM conversation; one accumulating file per task's planner
		opts.Transcript = p.tx
		opts.SessionID = fmt.Sprintf("exp%d-planner", ts.ID())
	}
	// 实验功能:开启后由 noa 接管上下文压缩(归档集中在 <workDir>/noa/<SessionID> 下,持久)。
	noaSession := fmt.Sprintf("exp%d-planner", ts.ID())
	enableNoa(&opts, p.noaEnabledFn, p.workDir, noaSession, noaWarn(noaSession))
	// 态势（刚完成的意图 + 完整图）现在拼进本轮 user 输入（见下方 input）。user 里还有
	// 指令 + 跨唤醒待办（todo 是模型自己的规划便签，可再生，放 user 即可）。
	// 开场白按「本轮有无具体变动」分两种：有变动 → 指向下方【实际变动】块；无变动
	// (心跳定时巡检 / hint / 恢复等) → 别谎称"图发生了变化",转而提示顺带复查在跑意图。
	lead := "直前に具体的な変動がありました（下の【今回のラウンドをトリガーした実際の変動】を参照）。これに基づいて次のステップを計画してください："
	if len(triggers) == 0 {
		lead = "今回のラウンドは**定期巡回（ハートビート到来）/具体的な変動シグナルなし**によるウェイクアップです——グラフに新しい変動があるとは限りません。あわせて実行中のインテントを再確認してください：長時間進捗がない、または方向性がずれているものは steer_work で軌道修正し、方向性自体が完全に間違っているものは kill_work で損切りした上で、目標を判定し、方向性を補足するか決定してください："
		// 心跳/无变动唤醒时,若全图已无任何 open 或 running 意图 → 探索已停摆(没 worker 在跑、
		// 也没排队方向)。明确告知 planner 并强制其本轮补出新方向,别只复查在跑意图后空转一轮。
		if active, err := ts.HasActiveIntent(); err == nil && !active {
			lead = "今回のラウンドは**定期巡回（ハートビート到来）**によるウェイクアップであり、かつ現在**open または running のインテントが 1 つもありません**——実行中の worker もキュー待ちの方向性もなく、探索が停止しています。あなたは今回のラウンドで、目標達成に向けて前進し、かつグラフ内の既存インテントと**重複しない**新しいインテントを 1 つ以上**必ず**生成しなければなりません（0 インテントの出力は禁止です）。まず以下の状況に基づいて目標が達成されたか判定し、未達成であれば直ちに方向性を補足してください："
		}
	}
	input := lead + situational + "\n\n上記の状況に基づき、目標を判定してください。目標が【本当に達成された】（目標成果物を取得済み/目標の脆弱性を確認済み）場合は、prove_goal で 1 つずつマークしてください。**絶対条件：目標がまだ達成されておらず、かつ現在 open または running のインテントが 1 つもない（frontier_open=0 かつ running_intents が空）場合、今回のラウンドでは目標達成に向けて前進するインテントを必ず最低 1 つ生成しなければなりません——この時点では結果を待つべき実行中の work もキュー待ちの方向性もないため、0 インテントの出力はタスクの停止を意味します。すでに open/running のインテントが進行中であるか、目標が達成された場合にのみ、新しいインテントを生成せずに今回のラウンドを終了できます。**" +
		renderPlannerTodos(opts.Todos.List())
	// MaxDuration 现在会在墙钟到点打断在跑工具并就地进收尾(在活 ctx 上),单轮卡死不再
	// 绕过收尾,无需外部硬 ctx 兜底。ctx 只承载 pause / kill / shutdown。
	_, _, err = captureRun(ctx, opts, input,
		func(r db.Activity) {
			if emit != nil {
				r.Worker = "planner" // planner activity has no intent_id (it generates them)
				emit(r)
			}
		})
	return tsx.GoalMet, tsx.Reason, err
}
