package agent

import (
	"strings"

	"github.com/Autumn-27/artex/db"
)

// constraintBlock renders this task's operation constraints (task_constraints) as a
// high-priority block appended to the planner/worker system prompt. allow/deny are
// grouped; empty string when there are no constraints (or ts is nil). The framing
// deliberately puts these ABOVE the exploration/expansion heuristics so a declared
// boundary wins the tug-of-war against "chase another entry surface".
func constraintBlock(ts *db.ExplorationStore) string {
	if ts == nil {
		return ""
	}
	rows, err := ts.ListConstraints()
	if err != nil || len(rows) == 0 {
		return ""
	}
	var allow, deny []string
	for _, c := range rows {
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		if c.Kind == "allow" {
			allow = append(allow, "- "+text)
		} else {
			deny = append(deny, "- "+text)
		}
	}
	if len(allow) == 0 && len(deny) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【操作制約（最高優先度、以下のあらゆる探索・攻撃面拡大ヒューリスティックに優先します。インテントを生成するたび、またアクションを実行するたびに違反がないか自己点検し、違反する場合は実行してはなりません）】：")
	if len(allow) > 0 {
		b.WriteString("\n許可された操作：\n")
		b.WriteString(strings.Join(allow, "\n"))
	}
	if len(deny) > 0 {
		b.WriteString("\n禁止された操作：\n")
		b.WriteString(strings.Join(deny, "\n"))
	}
	b.WriteString("\n（制約外の新しいターゲット/新しいポート/新しいホストを発見しても、権限が付与されたことにはなりません。上記の許可範囲内に収まらない限り、out-of-scope のファクトとして記録してスキップし、そのためのインテント派生やアクション実行は行わないでください。）")
	return b.String()
}
