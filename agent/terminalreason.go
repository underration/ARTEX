package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Autumn-27/norma/harness"
)

// runTrace retains the latest tool call so an interrupted run can identify the
// operation that was still in flight.
type runTrace struct {
	startedAt time.Time
	id        string
	name      string
	input     string
	at        time.Time
	pending   bool
}

func (t *runTrace) start(id, name, input string) {
	t.id, t.name, t.input, t.at, t.pending = id, name, input, time.Now(), true
}

func (t *runTrace) done(id string) {
	if id == t.id {
		t.pending = false
	}
}

var reasonHint = map[harness.TerminalReason]string{
	harness.ReasonCompleted:         "モデルは今回のラウンドを正常に終了しましたが、テキストによるまとめを残しませんでした。ファクトとアセットは今回のツール呼び出し履歴を基準とします",
	harness.ReasonMaxTurns:          "ステップ数上限 (MaxTurns) に達しました：SDK が終了処理を実行しファクトとアセットを書き戻しました。インテントは失敗としてではなく、プランナーが方向性を変えて継続できるよう exhausted としてマークされます",
	harness.ReasonTimeout:           "単一実行のウォールクロック予算 (MaxDuration) に達しました：時間到来により実行中のツールを中断してその場で終了処理に入り、識別済みのファクトとアセットを書き戻しました。インテントは exhausted としてマークされます",
	harness.ReasonModelError:        "モデルまたは API 呼び出しが失敗しました（ネットワーク、認証、レート制限、プロバイダーの 5xx など）。リトライ上限に達した後、インテントは blocked としてマークされます——トランスポート層の障害によりこのインテントは実質的に探索できていません。実行過程 (get_worker_trace) を確認してから再発行または手法変更を決定してください",
	harness.ReasonBlockingLimit:     "コンテキスト長がハード上限に達したため、リクエスト送信前にブロックされました。インテントの粒度を絞るか、ツールの返却値を圧縮してください",
	harness.ReasonPromptTooLong:     "プロンプトが長すぎ、かつコンテキスト圧縮のリトライも使い果たしたため、実行を継続できません",
	harness.ReasonImageError:        "現在のモデルは今回のマルチモーダルコンテンツに対応していません。ビジョン対応モデルに切り替えるか、ツールが画像を返さないようにしてください",
	harness.ReasonStopHookPrevented: "Stop フックが今回のラウンドの終了を阻止し、その後継続できなくなりました。タスクの Guard ルールが厳しすぎないか確認してください",
	harness.ReasonHookStopped:       "スコープ外のターゲットや禁止コマンドなどにより、ツールまたはフックが実行の継続を能動的に停止しました。最後の tool_result のインターセプト説明を確認してください",
	harness.ReasonAbortedStreaming:  "モデルのストリーミング出力生成フェーズで実行がキャンセルされました",
	harness.ReasonAbortedTools:      "ツール実行フェーズで実行がキャンセルされました",
}

// terminalText renders a terminal event with no final text into a compact summary
// and a Markdown detail block.
func terminalText(ctx context.Context, term *harness.Terminal, tr *runTrace) (string, string) {
	reason := term.Reason
	aborted := reason == harness.ReasonAbortedStreaming || reason == harness.ReasonAbortedTools
	// Prompt may return ctx.Err directly without a terminal event. Preserve the
	// cancellation cause instead of falling back to an empty/unknown terminal reason.
	if reason == "" && ctx.Err() != nil {
		aborted = true
	}

	var sum string
	if aborted {
		_, short, _, ok := AbortReason(ctx)
		if !ok {
			short = "キャンセル原因を取得できませんでした"
		}
		stage := "実行中"
		switch reason {
		case harness.ReasonAbortedStreaming:
			stage = "モデル出力フェーズ"
		case harness.ReasonAbortedTools:
			stage = "ツール実行フェーズ"
		}
		sum = "（実行が中断されました：" + short + "；" + stage + "で停止" + progressSuffix(term, tr) + "、未完了）"
	} else if reason == harness.ReasonMaxTurns || reason == harness.ReasonTimeout {
		sum = "（実行予算上限(" + string(reason) + ")に達し、終了処理にてファクトを書き戻しました" + progressSuffix(term, tr) + "；今回はテキストのまとめなし）"
	} else {
		hint := terminalReasonHint(reason)
		sum = "（テキストのまとめなし、終了状態 " + terminalReasonLabel(reason) + "：" + firstLine(hint, 80) + "）"
	}

	var b strings.Builder
	b.WriteString(sum)
	b.WriteString("\n\n")
	displayReason := terminalReasonLabel(reason)
	fmt.Fprintf(&b, "- **終了状態**: `%s` - %s\n", displayReason, terminalReasonHint(reason))
	if aborted {
		code, _, why, ok := AbortReason(ctx)
		if ok {
			fmt.Fprintf(&b, "- **中断原因** (`%s`): %s\n", code, why)
		} else {
			b.WriteString("- **中断原因**: 取得できません。キャンセル元が context.WithCancelCause で名前付き原因を付与していない可能性があります\n")
		}
	}
	if term.Err != nil {
		fmt.Fprintf(&b, "- **基底エラー**: `%v`\n", term.Err)
	}
	if aborted && strings.TrimSpace(term.Text) != "" {
		b.WriteString("- **キャンセル前に生成された部分出力**:\n\n")
		b.WriteString(term.Text)
		b.WriteString("\n\n")
	}
	if term.Turns > 0 {
		fmt.Fprintf(&b, "- **実行済み**: %d モデルターン\n", term.Turns)
	}
	if !tr.startedAt.IsZero() {
		fmt.Fprintf(&b, "- **今回の実行時間**: %s\n", roundDur(time.Since(tr.startedAt)))
	}
	if u := term.Usage; u.InputTokens+u.OutputTokens+u.CacheReadTokens+u.CacheWriteTokens > 0 {
		fmt.Fprintf(&b, "- **累積 token**: 入力 %d / 出力 %d / キャッシュ読込 %d / キャッシュ書込 %d\n",
			u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
	}
	if tr.name == "" {
		b.WriteString("- **ツール呼び出し**: 今回の実行ではツール呼び出しが行われる前に終了しました\n")
	} else if tr.pending {
		fmt.Fprintf(&b, "- **中断時に実行中だったツール**: `%s`（実行経過 %s、**結果未返却**）\n\n  ```json\n  %s\n  ```\n",
			tr.name, roundDur(time.Since(tr.at)), firstLine(tr.input, 300))
	} else {
		fmt.Fprintf(&b, "- **中断前の最後のツール**: `%s`（正常に返却済み）\n", tr.name)
	}
	return sum, b.String()
}

func terminalReasonLabel(reason harness.TerminalReason) string {
	if reason == "" {
		return "context_canceled"
	}
	return string(reason)
}

func terminalReasonHint(reason harness.TerminalReason) string {
	if hint := reasonHint[reason]; hint != "" {
		return hint
	}
	if reason == "" {
		return "実行の context はキャンセルされましたが、基底層で Terminal イベントが生成されませんでした"
	}
	return "不明な終了状態。harness に新しい TerminalReason が追加された可能性があるため、reasonHint を補足してください"
}

func progressSuffix(term *harness.Terminal, tr *runTrace) string {
	var parts []string
	if term.Turns > 0 {
		parts = append(parts, fmt.Sprintf("%d ターン", term.Turns))
	}
	if !tr.startedAt.IsZero() {
		parts = append(parts, roundDur(time.Since(tr.startedAt)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "、実行経過 " + strings.Join(parts, " / ")
}

func roundDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return d.Round(100 * time.Millisecond).String()
	case d < time.Hour:
		return d.Round(time.Second).String()
	default:
		return d.Round(time.Minute).String()
	}
}
