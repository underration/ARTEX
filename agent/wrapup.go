package agent

import (
	"strings"

	"github.com/Autumn-27/norma/harness"
)

// 收尾提示词(wrap-up / settlement prompt):当 agent 因【步数耗尽(MaxTurns)】或
// 【超时(run_seconds/MaxDuration)】被终止时,SDK 的 settlement 阶段会注入这段提示,
// 让 agent 先把已识别但未写回的内容落库、再输出一句总结,避免烂尾。
//
// 每个 agent 的收尾提示词可在后台按需覆盖(存 agents.wrapup_prompt),留空则用这里的
// 内置默认。仅【提示词正文】可编辑;禁用哪些工具、收尾自身给几轮预算属代码固定策略。

// WrapupOverride, if set, returns the stored wrap-up prompt for an agent key and
// whether a non-empty one exists. Wired by the server to the agents table (like
// PromptOverride for system prompts). nil / empty → the built-in default is used.
var WrapupOverride func(agentKey string) (string, bool)

// WrapupMaxTurnsOverride, if set, returns the admin-configured turn budget for the
// wrap-up phase of an agent and whether a positive one exists. Wired to the agents
// table. nil / ≤0 → the built-in per-agent default (wrapupTurnDefaults) is used.
var WrapupMaxTurnsOverride func(agentKey string) (int, bool)

// 内置默认收尾提示词,按 agent key 索引。worker 复用历史上硬编码的 settleWrapUpPrompt
// (定义在 worker.go),planner/mainagent 各有一版;未命中的(自定义 agent)走通用兜底。
var wrapupDefaults = map[string]string{
	"worker":    settleWrapUpPrompt,
	"planner":   plannerWrapUpDefault,
	"mainagent": mainAgentWrapUpDefault,
}

// wrapupTurnDefaults: 各 agent 收尾阶段【自身】的轮数预算内置默认(可被后台 >0 覆盖)。
// 均给 10 轮,保证收尾阶段有足够步数落库。未命中走 genericWrapupTurns。
var wrapupTurnDefaults = map[string]int{
	"worker":    10,
	"planner":   10,
	"mainagent": 10,
}

const genericWrapupTurns = 10

const plannerWrapUpDefault = "今回の計画ラウンドのステップ数が間もなく上限に達します——これはあくまで【今回のラウンド】の終了であり、状況が変化すればシステムは再びあなたをウェイクアップして計画を継続するため、タスク自体の終了ではなく、ここで計画全体を締めくくる必要はありません。今回のラウンドでまとまった結論を確実に反映させて無駄にしないようにしつつ、【締めくくりのために無理やりインテントを捻り出さないでください】（今回のラウンドで 0 インテントとなるのは完全に正常な結果です）：(1) 【今すぐ発行すべき】と判断した探索方向がある場合は、1 回の add_intent で一括送信してください（決まったものは出し惜しみしないこと）；(2) ある検出結果/ファクトによって達成が証明された目標に対しては、prove_goal を呼び出して met にマークしてください（判定漏れのないように）；(3) 段階的に進めるべき直列エクスプロイトチェーンを識別した場合は、次回のウェイクアップで続きを発行できるよう TodoWrite に記録してください。完了したらまとめのテキストを出力せず、そのまま今回のラウンドを終了してください。"

const mainAgentWrapUpDefault = "ステップ数が間もなく上限に達し、今回の対話が終了します。新たな探索や操作は開始しないでください。現在の進捗、主要な結論、および推奨される次のステップを**1 文のプレーンテキストのみで**ユーザーに要約して伝えてください。"

const genericWrapUpDefault = "予算上限に達したため間もなく終了します。完了しているもののまだ DB に保存していない結果を先に書き戻してから、何を行い、どのような主要な結論が得られたかを**1 文のプレーンテキストのみで**要約してください（この文が今回の実行結果として表示されます）。"

// WrapupDefault returns the built-in default wrap-up prompt for an agent key —
// used by the admin UI as the "restore default" value and empty-field placeholder.
func WrapupDefault(agentKey string) string {
	if d, ok := wrapupDefaults[agentKey]; ok {
		return d
	}
	return genericWrapUpDefault
}

// WrapupTurnsDefault returns the built-in wrap-up turn budget for an agent key —
// used by the admin UI as the "0 = default N" hint.
func WrapupTurnsDefault(agentKey string) int {
	if n, ok := wrapupTurnDefaults[agentKey]; ok {
		return n
	}
	return genericWrapupTurns
}

// resolveWrapup returns the effective wrap-up prompt: the DB override (if set and
// non-empty) over the built-in default.
func resolveWrapup(agentKey string) string {
	if WrapupOverride != nil {
		if t, ok := WrapupOverride(agentKey); ok && strings.TrimSpace(t) != "" {
			return t
		}
	}
	return WrapupDefault(agentKey)
}

// resolveWrapupTurns returns the effective wrap-up turn budget: a positive DB
// override over the built-in per-agent default.
func resolveWrapupTurns(agentKey string) int {
	if WrapupMaxTurnsOverride != nil {
		if v, ok := WrapupMaxTurnsOverride(agentKey); ok && v > 0 {
			return v
		}
	}
	return WrapupTurnsDefault(agentKey)
}

// wrapupSettlement builds the settlement config for an agent's run. Prompt and the
// turn budget are admin-editable per agent; disabled tools are code-owned policy so
// a user can't edit away the "stop probing" guardrail. Resolved fresh each run
// (reads DB live), so edits apply on the next run without a restart.
func wrapupSettlement(agentKey string, disabledTools []string) *harness.Settlement {
	return &harness.Settlement{
		Prompt:        resolveWrapup(agentKey),
		DisabledTools: disabledTools,
		MaxTurns:      resolveWrapupTurns(agentKey),
	}
}

// ---------- 任务级超时收尾词（见 docs/任务级超时与收尾设计.md）----------
//
// 与 per-run 收尾词是【两套】：per-run 是"你这一次 run 的预算用完了"；任务超时是
// "整个任务到点、即将结束"。语义常相反（尤其 planner：per-run 说"别停继续规划"，
// 任务超时说"到点停止规划、做最后判定"）。只给 worker/planner 配置。

// WrapupTaskTimeoutOverride / …TurnsOverride：任务超时收尾词与轮数的 DB 覆盖
// （wire 到 agents.task_timeout_wrapup_prompt / _max_turns，仅 worker/planner）。
var (
	WrapupTaskTimeoutOverride      func(agentKey string) (string, bool)
	WrapupTaskTimeoutTurnsOverride func(agentKey string) (int, bool)
)

var taskTimeoutWrapupDefaults = map[string]string{
	"worker":  workerTaskTimeoutDefault,
	"planner": plannerTaskTimeoutDefault,
}

const workerTaskTimeoutDefault = "**タスク全体がタイムアウト上限に達し、間もなく終了します**（今回の run の予算ではなく、探索全体の終了時刻に達しました）。これが最後の機会です：(1) すでに識別したもののまだ書き戻していない内容を【すべて】DB に記録してください——新しいアセットは insert_assets、探索の結論/ファクトは record_fact、確認された脆弱性は report_finding；(2) 新しいコマンドやプローブは一切開始しないでください；(3) **最後に 1 文のプレーンテキストのみで**、本インテントにおける主要な結論を要約してください。"

const plannerTaskTimeoutDefault = "**タスク全体がタイムアウト上限に達し、間もなく終了します**（今回のラウンドだけでなく、タスク全体が終了します）。現在の【すべての】ファクトと検出結果に基づき、最後の目標判定を行ってください：証拠によって達成が証明された目標に対しては prove_goal を呼び出して met にマークしてください（判定漏れのないように）。**新しいインテントは一切生成しないでください**（この時点でインテントを発行しても実行されません）。判定が終われば完了とし、まとめのテキストを出力する必要はありません。"

// TaskTimeoutWrapupDefault 返回某 agent 的任务超时内置默认收尾词（供后台占位/恢复默认）。
func TaskTimeoutWrapupDefault(agentKey string) string {
	return taskTimeoutWrapupDefaults[agentKey] // 未配置(mainagent/chat)返回空串
}

// resolveTaskTimeoutWrapup：DB 覆盖(非空) > 内置默认。空串表示该 agent 无任务超时词
// （非 worker/planner），此时调用方应回退 per-run 词。
func resolveTaskTimeoutWrapup(agentKey string) string {
	if WrapupTaskTimeoutOverride != nil {
		if t, ok := WrapupTaskTimeoutOverride(agentKey); ok && strings.TrimSpace(t) != "" {
			return t
		}
	}
	return TaskTimeoutWrapupDefault(agentKey)
}

func resolveTaskTimeoutTurns(agentKey string) int {
	if WrapupTaskTimeoutTurnsOverride != nil {
		if v, ok := WrapupTaskTimeoutTurnsOverride(agentKey); ok && v > 0 {
			return v
		}
	}
	return resolveWrapupTurns(agentKey) // 默认沿用 per-run 轮数
}

// wrapupSettlementForTask builds settlement for a worker/planner run that is aware
// of the task deadline. See §5 of the design doc:
//   - clamped=true  → 本次 run 被任务 deadline 夹逼：因 Timeout 收尾=任务到点→任务超时词；
//     因 MaxTurns 收尾=夹逼窗口内步数先耗尽、任务还剩几分钟→回落 per-run 词。
//   - clamped=false → 任务还早：两种 reason 都用 per-run 词（即退化为 wrapupSettlement）。
//
// 交给 harness 的 PromptByReason 在收尾时按【实际】reason 现场挑，无 build 时错配。
func wrapupSettlementForTask(agentKey string, disabledTools []string, clamped bool) *harness.Settlement {
	perRun := resolveWrapup(agentKey)
	st := &harness.Settlement{
		Prompt:        perRun, // 兜底(也是非 clamped 时两种 reason 的取值)
		DisabledTools: disabledTools,
		MaxTurns:      resolveWrapupTurns(agentKey),
	}
	if clamped {
		if tt := resolveTaskTimeoutWrapup(agentKey); tt != "" {
			st.PromptByReason = map[harness.TerminalReason]string{
				harness.ReasonTimeout:  tt,     // 任务到点
				harness.ReasonMaxTurns: perRun, // 步数先耗尽、任务还剩时间
			}
			st.MaxTurns = resolveTaskTimeoutTurns(agentKey)
		}
	}
	return st
}
