package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/traffic"
	actool "github.com/Autumn-27/norma/tool"
)

func (s *Server) seedFindingWorkflowTools() {
	const hostSearchDescriptionFlag = "finding_workflow_tools_v3_host_search_description"
	if value, _, _ := s.m.pg.GetSetting(hostSearchDescriptionFlag); value != "true" {
		// Only replace the original built-in text. A user-edited description is
		// authoritative and must survive upgrades.
		legacy := "記録プロキシがキャプチャ済みの対象トラフィックを検索します（host の指定が必須。さらに URL の部分文字列やボディのキーワードでフィルタ可能）。body_contains はキャプチャ済みのリクエスト/レスポンスヘッダーおよびボディに対して全文検索を行い、任意の部分文字列や日本語（3 文字以上）に対応し、レスポンス内のパスワード、秘密鍵、エラーメッセージ、内部 IP アドレスなどの検索に利用できます。極めて軽量なインデックス(id/method/url/status/resp_len)のみを返し、レスポンス内容は一切含みません。デフォルトで 3 件、1 ページ最大 10 件を返します。結果が多い場合は page でページング（page=0 から）し、特定のリクエスト/レスポンスの原文を確認するには traffic_get(id) を使用してください。アクセス済みリソースの確認やエンドポイントの探索にはまずこれを使用し、同じ URL への重複した curl を避けてください。"
		if _, err := s.m.pg.Exec(`UPDATE tools SET description=$1,updated_at=now() WHERE key='traffic_search' AND system AND description=$2`, traffic.TrafficSearchDescription, legacy); err != nil {
			// Log and leave the flag unset so the next startup retries; do not
			// return, or a transient error here would also skip the reporter
			// migration below — the two are independent.
			log.Printf("[evidence] upgrade traffic_search description: %v", err)
		} else {
			_ = s.m.pg.SetSetting(hostSearchDescriptionFlag, "true")
		}
	}
	const flag = "finding_workflow_tools_v2_reporter"
	if value, _, _ := s.m.pg.GetSetting(flag); value == "true" {
		return
	}
	for _, key := range []string{"report_finding", "add_hint", "add_task_hint"} {
		row, err := s.m.pg.GetTool(key)
		if err != nil {
			log.Printf("[evidence] load %s: %v", key, err)
			return
		}
		if row == nil || !row.System {
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(row.Schema, &schema); err != nil {
			log.Printf("[evidence] invalid schema for %s: %v", key, err)
			return
		}
		if schema == nil {
			log.Printf("[evidence] missing object schema for %s", key)
			return
		}
		props := objectProperty(schema, "properties")
		if key == "report_finding" {
			if _, exists := props["evidence_hint_id"]; !exists {
				props["evidence_hint_id"] = map[string]any{"type": "integer", "description": "任意：このタスク内で本脆弱性に対応する hint ID。そのヒントに保存された traffic_refs を読み取って一括バインドします。ヒントがない場合は省略"}
			}
		} else {
			if _, exists := props["traffic_refs"]; !exists {
				props["traffic_refs"] = agent.HintTrafficSchema()
			}
			hints := objectProperty(props, "hints")
			if _, exists := hints["type"]; !exists {
				hints["type"] = "array"
			}
			items := objectProperty(hints, "items")
			if _, ok := items["type"]; !ok {
				items["type"] = "object"
			}
			itemProps := objectProperty(items, "properties")
			for name, value := range map[string]any{"text": strParam("ヒント内容"), "asset_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}, "traffic_refs": agent.HintTrafficSchema()} {
				if _, exists := itemProps[name]; !exists {
					itemProps[name] = value
				}
			}
		}
		raw, _ := json.Marshal(schema)
		result, err := s.m.pg.Exec(`UPDATE tools SET schema=$2::jsonb,updated_at=now() WHERE key=$1 AND system AND schema=$3::jsonb`, key, string(raw), string(row.Schema))
		if err != nil {
			log.Printf("[evidence] upgrade %s: %v", key, err)
			return
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return
		} // preserve concurrent user edits
	}
	// Upgrade only the original default binding. Customized lists and enabled
	// flags survive; the one-time flag also preserves future user unbinding.
	readers := `["worker","reporter"]`
	for _, key := range []string{"traffic_search", "traffic_get", "traffic_blob"} {
		if _, err := s.m.pg.Exec(`UPDATE tools SET agents=$2::jsonb WHERE key=$1 AND system AND (agents='["worker"]'::jsonb OR (agents @> '["worker","planner","mainagent","auto","pentest"]'::jsonb AND jsonb_array_length(agents)=5))`, key, readers); err != nil {
			return
		}
	}
	if _, err := s.m.pg.Exec(`UPDATE tools SET agents=$1::jsonb WHERE key='get_finding_traffic' AND system AND agents @> '["auto","reporter"]'::jsonb AND jsonb_array_length(agents)=2`, `["auto","reporter","worker","planner","mainagent","pentest"]`); err != nil {
		return
	}
	// Replace the previous code default only; preserve customized binding lists.
	if _, err := s.m.pg.Exec(`UPDATE tools SET agents='["reporter"]'::jsonb WHERE key='bind_finding_traffic' AND system AND agents @> '["worker","planner","mainagent","auto","pentest"]'::jsonb AND jsonb_array_length(agents)=5`); err != nil {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("reporter", []string{"bind_finding_traffic"}); err != nil {
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

func objectProperty(parent map[string]any, key string) map[string]any {
	value, ok := parent[key].(map[string]any)
	if !ok {
		value = map[string]any{}
		parent[key] = value
	}
	return value
}

func (s *Server) agentFindingTrafficAccess(ctx context.Context, id int64, write bool) error {
	if id <= 0 {
		return errors.New("finding_id は独立した脆弱性レコード ID である必要があります（探索ノード ID ではありません）")
	}
	f, err := s.m.pg.GetFinding(id)
	if err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("%w：finding_id=%d。証跡ツールには独立した脆弱性レコード ID を使用します。list_task_findings / get_task_node_detail の finding_id フィールドから読み取ってください。id / finding_node_id は渡さないでください", db.ErrFindingNotFound, id)
	}
	if ri := agent.RunInfoFrom(ctx); ri.TaskID > 0 {
		task := s.m.ResolveTask(strconv.FormatInt(ri.TaskID, 10))
		if task == nil {
			return errors.New("タスクが存在しません")
		}
		_, inherited, allowed := findingProvenanceInTask(task, f.TaskID)
		if !allowed {
			return errors.New("現在のタスクからはこの脆弱性を読み取れません")
		}
		if write && inherited {
			return errors.New("継承された脆弱性のトラフィック証跡は読み取り専用です。元のタスクで変更してください")
		}
	}
	return nil
}

func (s *Server) toolBindFindingTraffic() actool.CoreTool {
	return wrTool("bind_finding_traffic", "登録済みの脆弱性に対して、確認済みの実際の HTTP トラフィックを追加バインドします。finding_id には独立した脆弱性レコード ID を使用し、探索ノード ID は渡さないでください。同一バッチの参照はすべて成功するかすべて失敗し、重複する参照は既存の説明を上書きしません。追加バインドにより既存のレポートは更新待ちとしてマークされます。トラフィック補完のために再スキャンしたり脆弱性を重複作成したりしないでください。",
		objSchema(map[string]any{"finding_id": strParam("独立した脆弱性レコード ID（list_task_findings / get_task_node_detail の finding_id フィールドから読み取る）"), "traffic_refs": agent.HintTrafficSchema()}, "finding_id", "traffic_refs"),
		func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
			if !s.m.pg.GetBool(settingAgentTrafficBinding, false) {
				return actool.Errorf("Agent によるトラフィック自動バインドは無効です。システム設定で有効にするか、画面から手動でバインドしてください。"), nil
			}
			var args struct {
				FindingID json.RawMessage `json:"finding_id"`
				Refs      []db.TrafficRef `json:"traffic_refs"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			id := parseProfileID(args.FindingID)
			if err := s.agentFindingTrafficAccess(ctx, id, true); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if len(args.Refs) == 0 {
				return actool.Errorf("追加バインドには確認済みの traffic_refs が少なくとも 1 件必要です。トラフィックがない場合はこのツールを呼び出す必要はありません"), nil
			}
			list, err := s.evidenceStore().Bind(ctx, id, args.Refs)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(trafficSummary(list))
		})
}
