package agent

import (
	"context"

	"github.com/Autumn-27/artex/db"
)

// FindingRecorder is injected by the host; agents never synthesize or copy
// evidence bodies themselves. Its implementation owns the atomic write.
type FindingRecorder interface {
	Record(context.Context, db.RecordFindingInput, []db.TrafficRef) (*db.RecordedFinding, error)
}

// Tool-use guidance is appended without replacing the user's editable prompt.
// It does not require capture or claim that unavailable traffic tools exist.
const findingTrafficGuidance = "\n\n**脆弱性トラフィック証拠（任意）**：report_finding を呼び出して脆弱性を報告する際、すでに確認済みで脆弱性の結論を裏付ける HTTP リクエスト/レスポンスがある場合は、traffic_refs を用いて再現順に実際の ID を紐付けることができます（ドメイン名と時刻は候補の絞り込みにのみ使用し、関連性を推定してはなりません）。TCP などの非 HTTP 脆弱性、未収集、または明確な一致がない場合は省略するか [] を渡し、evidence にコマンド出力やログなどの検証可能な他の証拠を残した上で、紐付けなかった理由を説明することを推奨します。ID を推測したり、パケット補完のためだけに再プローブを行ったりしないでください。"

func (t *ToolSet) SetFindingRecorder(r FindingRecorder)   { t.findingRecorder = r }
func (w *Worker) SetFindingRecorder(r FindingRecorder)    { w.findingRecorder = r }
func (p *Planner) SetFindingRecorder(r FindingRecorder)   { p.findingRecorder = r }
func (m *MainAgent) SetFindingRecorder(r FindingRecorder) { m.findingRecorder = r }
