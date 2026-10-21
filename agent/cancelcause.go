package agent

import (
	"context"
	"errors"
	"fmt"
)

// AbortCause names why an agent run's context was cancelled. Every cancellation
// site should attach one so the activity trace can report the real initiator.
type AbortCause struct {
	Code  string
	Short string
	Text  string
}

func (c *AbortCause) Error() string { return c.Text }

func cause(code, short, text string) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: text}
}

// Causef builds a cause that includes runtime-specific detail.
func Causef(code, short, format string, args ...any) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: fmt.Sprintf(format, args...)}
}

var (
	// Task-level execution context.
	AbortPausedByUser = cause("paused_by_user", "ユーザーがタスクを一時停止しました",
		"ユーザーがタスク制御 API（POST /api/tasks/{id}/control，action=pause）を通じてタスクを一時停止しました。今回の Planner/Worker 実行は能動的にキャンセルされ、実行中のインテントは frontier(open) に戻され、タスク再開後に再度取得されて最初から実行されます")
	AbortPausedByOrchestrator = cause("paused_by_orchestrator", "オーケストレーション Agent がタスクを一時停止しました",
		"オーケストレーション Agent が pause_task ツールを呼び出して本タスクを一時停止しました。今回の Planner/Worker 実行は能動的にキャンセルされ、実行中のインテントは frontier(open) に戻され、再開後に再実行されます")
	AbortTaskDeleted = cause("task_deleted", "タスクが削除されました",
		"タスクが削除処理中（DELETE /api/tasks/{id}）であり、削除バリアによって当該タスクで実行中の Planner、Worker、およびメイン Agent がキャンセルされました。今回の実行結果は使用されません")
	AbortPausedOnReload = cause("paused_on_reload", "バックエンドがタスクの一時停止状態を復元しました",
		"バックエンド起動時にデータベース内の永続化状態に基づいてタスクの一時停止が復元されました。今回の実行はキャンセルされました（通常、復元フェーズでは実行中の Agent は存在しません）")
	AbortGoalMet = cause("goal_met", "プランナーがタスク目標の達成を判定しました",
		"プランナーがタスク目標の達成を判定してタスクを done に設定し、その後も実行中の Worker をキャンセルしました。これらのインテントは失敗ではなく stopped としてマークされます")
	AbortSettleDrainTimeout = cause("settle_drain_timeout", "タスクタイムアウト終了処理の待機時間が上限に達しました",
		"タスクが timeout に達した後、実行中の Worker のグレースフルな終了処理を待機しましたが、90 秒の drain 猶予時間を超えたため強制キャンセルを実行しました。インテントは exhausted としてマークされ、終了処理フェーズですでに書き込まれたファクトとアセットは保持されます")

	// Per-work context.
	AbortKilledByPlanner = cause("killed_by_planner", "プランナーがこのインテントを終了しました",
		"プランナーが kill_work を呼び出してこのインテントを能動的に終了しました。通常は方向性のずれや継続価値の喪失を示します。インテントは stopped としてマークされ、自動的に再取得されることはありません")
	AbortWorkPausedByUser = cause("work_paused_by_user", "ユーザーがこの Worker インテントを一時停止しました",
		"ユーザーが実行中の Worker を一時停止しました。今回の呼び出しはキャンセルされ、インテントは paused に遷移します。すでに登録されたインテント、ファクト、脆弱性、アクティビティ履歴はすべて保持され、再開後に最初から再実行されます")
	AbortWorkCancelledByUser = cause("work_cancelled_by_user", "ユーザーがこの Worker インテントを削除しました",
		"ユーザーが実行中の Worker を削除しました。今回の呼び出しはキャンセルされ、Worker が書き込みセクションを抜けた後、サーバー側でユーザーが選択した削除モードに従って当該インテントを処理します——論理削除は削除済みとしてマークし全成果物を保持しますが、物理削除は当該インテントおよびそれのみによって支えられている下流ノードをカスケード削除します")
	AbortWorkFinished = cause("work_finished", "Worker が正常終了し context を解放しました",
		"Worker は正常に終了し、エンジンが detachWork 内でその context リソースを解放しました。これは実行の中断ではありません。中断メッセージ内に表示された場合は、キャンセルと終了イベントの間で競合が発生したことを示します")
	AbortPausedRaceGuard = cause("paused_race_guard", "タスク一時停止中の新規実行起動を拒否しました",
		"タスクが一時停止状態にあるため、claim と一時停止の間の競合によって Worker が起動し続けるのを防ぐべく、エンジンが新しい実行 context の発行を拒否しました。取得済みのインテントは frontier に戻されます")

	// Main Agent and standalone conversation contexts.
	AbortChatStoppedByUser = cause("chat_stopped_by_user", "ユーザーが今回の対話を停止しました",
		"ユーザーが停止をクリックし、今回のメイン Agent またはチャット Agent の実行を能動的に中止しました。すでに生成されたアクティビティ履歴は保持され、次のメッセージを引き続き送信できます")
	AbortChatPausedWithTask = cause("chat_paused_with_task", "タスクの一時停止に伴いメイン Agent の対話を中止しました",
		"ユーザーがタスクを一時停止した際、実行中のメイン Agent の対話も同期してキャンセルされました。すでに生成されたアクティビティ履歴は保持されますが、タスク再開後に今回のメッセージが自動的に再実行されることはありません")
	AbortChatTurnFinished = cause("chat_turn_finished", "今回の対話が正常終了し context を解放しました",
		"今回の対話は正常に終了し、サーバー側で当該ターンの context リソースを解放しています。これは実行の中断ではありません。中断メッセージ内に表示された場合は、キャンセルと終了イベントの間で競合が発生したことを示します")

	// Process-level and per-run hard backstop.
	AbortShutdown = cause("shutdown", "バックエンドプロセスがシャットダウン中です",
		"バックエンドプロセスが SIGINT または SIGTERM を受信し、再起動、更新、またはシャットダウンを行っています。実行中のすべての Agent はキャンセルされ、再起動後に残っている running インテントは open にリセットされて再実行されます")
	AbortRunHardTimeout = cause("run_hard_timeout", "単一実行のハードタイムアウト上限が発動しました",
		"単一実行がソフトウォールクロック予算および追加の猶予時間を超過しました。モデルリクエストまたは特定のツールが長時間応答せず、通常のターン境界での終了処理が実行できなかったことを示します。中断前の最後の未応答ツール呼び出しを重点的に確認してください")
)

// AbortReason resolves the named cause attached to a cancelled run context.
func AbortReason(ctx context.Context) (code, short, text string, ok bool) {
	c := context.Cause(ctx)
	if c == nil {
		return "", "", "", false
	}
	var ac *AbortCause
	if errors.As(c, &ac) {
		return ac.Code, ac.Short, ac.Text, true
	}
	switch {
	case errors.Is(c, context.DeadlineExceeded):
		return "deadline_exceeded", "上流 context が deadline に達しました",
			"上流 context が deadline に達しましたが、設定側で WithTimeoutCause による名前付き原因が付与されていません: " + c.Error(), true
	case errors.Is(c, context.Canceled):
		return "canceled_no_cause", "キャンセル元が名前付き原因を付与していません",
			"上流 context がキャンセルされましたが、キャンセル元で context.WithCancelCause による名前付き原因が付与されていません。agent/cancelcause.go に原因を登録し、該当キャンセル箇所に接続してください", true
	default:
		return "other", firstLine(c.Error(), 80), c.Error(), true
	}
}
