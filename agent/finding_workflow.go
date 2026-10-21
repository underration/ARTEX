package agent

import (
	"encoding/json"
	"fmt"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

const findingIDGuidance = "\n\n**脆弱性 ID の規約**：finding_id は独立した脆弱性レコード ID であり、finding_node_id は探索ノード ID です。list_findings / list_task_findings / node_detail / get_task_node_detail の id は探索ノード ID のまま保持されるため、同じレスポンス内の finding_id から独立 ID を読み取ってください。get_finding_traffic / bind_finding_traffic には独立した finding_id を使用します。旧来の update_finding_report の finding_id 引数には引き続き finding_node_id を渡します。report_finding の 1 行目の数値を証拠ツールに使用したり、ID エラー発生後に他の数値を推測したりしないでください。"

// The server supplies the persisted setting. A missing setting/host is off.
// Consulted at assembly and again on writes so an already-running session
// cannot keep binding after the user switches the feature off.
var FindingTrafficBindingEnabled func() bool

func findingTrafficBindingEnabled() bool {
	return FindingTrafficBindingEnabled != nil && FindingTrafficBindingEnabled()
}

// Applied after ToolResolve: user descriptions and prompts remain intact, while
// all actual reporters (including Planner and custom chat agents) see the same
// API contract. Disabled/unbound tools are never reintroduced here.
func findingWorkflowTools(agentKey string, tools []actool.CoreTool) ([]actool.CoreTool, string) {
	if !findingTrafficBindingEnabled() {
		out := make([]actool.CoreTool, 0, len(tools))
		for _, tool := range tools {
			if tool.Name() == "bind_finding_traffic" {
				continue
			}
			if agentKey == "reporter" && (tool.Name() == "traffic_search" || tool.Name() == "traffic_get" || tool.Name() == "traffic_blob") {
				continue
			}
			switch tool.Name() {
			case "report_finding", "add_hint", "add_task_hint":
				// Work on a copy: toggling back on must restore the original schema.
				raw, _ := json.Marshal(tool.InputSchema())
				var schema map[string]any
				if json.Unmarshal(raw, &schema) == nil {
					stripTrafficParameters(schema)
					tool = DecorateTool(tool, tool.Description(), schema)
				}
			}
			out = append(out, tool)
		}
		return out, ""
	}
	out := append([]actool.CoreTool(nil), tools...)
	has := map[string]bool{}
	for i, tool := range out {
		has[tool.Name()] = true
		note := ""
		switch tool.Name() {
		case "report_finding":
			note = "\nデフォルトでは、レポート Agent がレポート作成前にトラフィックを照合して紐付けます。報告者は、レポート Agent が実行履歴と照らし合わせて検証できるよう、evidence に検証コマンド、主要な出力、既存の実トラフィック ID とその用途を残してください（紐付けのために追加でパケットを検索する必要はありません）。明示的な即時紐付けにも対応しています：traffic_refs または evidence_hint_id で確認済みの参照を送信でき、後者は本タスクの指定した hint の構造化参照を読み取ります（いずれかが無効な場合、今回の報告はすべて失敗します）。TCP やパケットなしの場合はこれらの任意引数は不要です。返却される finding_id と finding_node_id はそれぞれ独立レコードと探索ノードを表します。"
		case "add_hint", "add_task_hint":
			note = "\n確認済みの脆弱性を引き継ぐ際は、対応するヒントの traffic_refs に確認済みトラフィックの ID、用途、説明、順序を保持し（単一の場合はトップレベル、一括の場合は対応する hints 要素内）、text でそれが証明する具体的な脆弱性を説明してください。呼び出し側はテキストだけを引き継いで既存のトラフィック参照を破棄してはなりません。未確認の候補を証拠として渡すことはできません。"
		case "get_finding_traffic", "bind_finding_traffic", "list_findings", "list_task_findings", "node_detail", "get_task_node_detail", "update_finding_report":
			note = findingIDGuidance
		}
		if note != "" {
			out[i] = DecorateTool(tool, tool.Description()+note, tool.InputSchema())
		}
	}
	guidance := ""
	if has["report_finding"] || has["add_task_hint"] || has["add_hint"] {
		guidance = "\n\n**トラフィック証拠の引き継ぎ（任意）**：自動紐付けはデフォルトで、脆弱性の DB 登録後・レポート作成前にレポート Agent によって完了されます。報告者はレポート Agent が追跡できるよう、evidence に検証コマンド、主要な出力、既存の実トラフィック ID とその用途を残し、タスク内では intent_id を付与してください（紐付けのために追加でパケットを検索する必要はありません）。Auto / Planner が代理報告する際も、実行者がすでに持っている参照を破棄しないでください。add_hint / add_task_hint は traffic_refs で引き継ぎ可能であり、明示的な即時紐付けも引き続き report_finding の traffic_refs / evidence_hint_id に対応しています。TCP やパケットがない場合は通常通り登録し、ID を推測したりパケット補完のためだけに再プローブしたりしないでください。"
		if has["add_task_hint"] && !has["add_hint"] {
			guidance += "\nプラットフォームのチャットにタスクコンテキストがない場合は、直接 report_finding を呼び出さず、add_task_hint を通じて既存の対応するタスクへ引き継ぎ、タスク Agent に登録させた上で list_task_findings で結果を確認してください。"
		}
		if has["prove_goal"] || has["goal_met"] {
			guidance += "\n目標達成を判定する前に、まず今回得られている証拠の報告・引き継ぎを完了してください。証拠の引き継ぎが未完了の段階で、テキスト上の脆弱性が登録されたことだけを理由にタスクを終了したり Worker をキャンセルしたりしないでください（パケットがない場合は待機や強制的なパケットキャプチャは不要です）。"
		}
	}
	if has["update_finding_report"] && has["bind_finding_traffic"] && has["get_finding_traffic"] {
		guidance += "\n\n**レポート作成前の自動トラフィック紐付け（有効）**：あなたは今回トリガーされた脆弱性についてトラフィックを照合・紐付けてからレポートを執筆する役割を担います。まず report_finding の返却 JSON または get_task_node_detail / list_task_findings から明確な finding_id と finding_node_id を取得してください。脆弱性の詳細、対応するインテントの実行履歴、および既存の証拠リストを読み取り、報告者から引き継がれた実際の ID を優先的に使用します。今回の検証が HTTP でありトラフィックツールが利用可能な場合は、traffic_search で候補を絞り込み、さらに traffic_get でリクエスト/レスポンスが当該脆弱性を裏付けているか 1 件ずつ確認してください（ドメイン名と時刻は絞り込みにのみ使用し、帰属の証明にはなりません）。確認された証拠を再現順に bind_finding_traffic(finding_id, traffic_refs) で紐付け、baseline / proof / verification / supporting を選択して用途を記述してください。今回の脆弱性のみを操作対象とし、脆弱性の重複作成やターゲットへの再プローブは行わないでください。紐付け成功後は再度 get_finding_traffic を呼び出して最新の version を取得し、必要な本文を読み取った上で、実際に読み取った version を evidence_version として update_finding_report に渡してください（その finding_id 引数には引き続き finding_node_id を使用します）。既存の紐付けを重複して追加する必要はありません。TCP、未収集、ツール利用不可、または明確な一致がない場合は自動紐付けをスキップし、テキスト/コマンド証拠に基づいて通常通りレポートを作成して理由を説明してください（トラフィックを揃えるために推測してはなりません）。紐付けに失敗した場合は成功と主張せず、既存の証拠を保持した上でレポートに未紐付けの理由を記載してください。"
	}
	if guidance != "" || has["get_finding_traffic"] || has["update_finding_report"] {
		guidance += findingIDGuidance
	}
	return out, guidance
}

func stripTrafficParameters(schema map[string]any) {
	props, _ := schema["properties"].(map[string]any)
	delete(props, "traffic_refs")
	delete(props, "evidence_hint_id")
	if required, ok := schema["required"].([]any); ok {
		kept := required[:0]
		for _, key := range required {
			if key != "traffic_refs" && key != "evidence_hint_id" {
				kept = append(kept, key)
			}
		}
		schema["required"] = kept
	}
	if hints, ok := props["hints"].(map[string]any); ok {
		if items, ok := hints["items"].(map[string]any); ok {
			stripTrafficParameters(items)
		}
	}
}

// HintTrafficSchema is shared by the task-local and cross-task hint tools.
func HintTrafficSchema() map[string]any {
	return map[string]any{"type": "array", "description": "任意：確認済みで本ヒント内の具体的な脆弱性に対応するトラフィック参照（順序を保持）。引き継ぎ後、report_finding に evidence_hint_id を渡すことでこれらの参照を引き継げます。", "items": obj(map[string]any{"traffic_id": str("実際のトラフィック ID"), "role": str("baseline / proof / verification / supporting"), "note": str("このトラフィックがどの結論を裏付けるか")}, "traffic_id")}
}

func (t *ToolSet) findingRefsFromHint(hintID int64, explicit []db.TrafficRef) ([]db.TrafficRef, error) {
	if hintID <= 0 {
		return db.NormalizeTrafficRefs(explicit)
	}
	n, err := t.ts.GetNode(hintID) // local store only: inherited hints cannot supply evidence
	if err != nil {
		return nil, err
	}
	if n == nil || n.Kind != db.KindHint {
		return nil, fmt.Errorf("evidence_hint_id=%d は本タスクのヒントノードである必要があります（継承されたヒントは直接紐付けに使用できません）", hintID)
	}
	var payload struct {
		Refs []db.TrafficRef `json:"traffic_refs"`
	}
	if err := json.Unmarshal(n.Payload, &payload); err != nil {
		return nil, err
	}
	return db.NormalizeTrafficRefs(append(append([]db.TrafficRef{}, explicit...), payload.Refs...))
}
