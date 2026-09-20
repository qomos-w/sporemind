package agent

import (
	"github.com/qomos-w/gospore/actor"
)

// phaseCommit 把引擎缓冲的 step/turn 事件 flush 到外部订阅者，
// 并在 turn 到达终态时结束本次运行。
//
// 它不负责产生新事件，只负责把已经产生的事件"落盘"出去，所以叫 Commit。
//
// 流程图：
//
//	┌─────────────────────────┐
//	│    flushEvents(ctx)     │ 把 step 事件全量刷出
//	└───────────┬─────────────┘
//	            ↓
//	┌─────────────────────────┐
//	│ loopState < Completed?  │ 仍运行中的 turn 做 checkpoint
//	└───────────┬─────────────┘
//	            ↓
//	┌─────────────────────────┐
//	│   flushTurnEvents(ctx)  │ 把 turn 级事件（含 turn.completed）刷出
//	└───────────┬─────────────┘
//	            ↓
//	┌─────────────────────────┐
//	│  loopState.IsTerminal() │
//	└───────────┬─────────────┘
//	        是 /   \ 否
//	          /     \
//	         ↓       ↓
//	 return (true, nil)  return (false, nil)
//	 退出 run             继续状态机切换
//
// 终态（Completed/Failed/Cancelled）不可复活。若有用户在 turn 完成期间发送的
// 新消息，引擎退出后由 agent 层通过新的 assistant TurnID 与不可变的 TurnOrder
// 启动后继 turn，旧的 terminal record 保持不变。
//
// 返回值语义：
//
//	done == true  → run() 主循环应当退出；
//	done == false → 还有下一轮，继续状态机切换。
func (e *turnEngine) phaseCommit(ctx actor.Context, turnID string) (bool, error) {
	// 先刷 step 事件：把 phaseDispatch / phaseExecute 期间 emitStepEvent 缓冲的事件
	// 通过 onStepEvent 回调或 childMode 下 onFlushProgress 投递出去。
	if err := e.flushEvents(ctx); err != nil {
		return false, err
	}

	// Mid-turn checkpoint: persist steps so crash recovery can restore them.
	// At this point all steps from the current iteration are closed (Dispatch→
	// Audit→Execute→Commit completed). Only checkpoint when the turn is still
	// running — terminal turns are persisted by handleTurnComplete.
	if !e.loopState.IsTerminal() && e.onCheckpoint != nil {
		e.onCheckpoint(ctx)
	}

	// 再刷 turn 事件：包括 turn.opened / turn.progress / turn.completed 等。
	if err := e.flushTurnEvents(ctx); err != nil {
		return false, err
	}

	// 如果当前已经到达 Completed / Failed / Cancelled 等终态，本轮 run 结束。
	// 终态不可复活：用户新消息由 agent 层在引擎退出后启动新的 assistant
	// TurnID/TurnOrder 处理，而不是复用本 turn 的 engine loop。
	if e.loopState.IsTerminal() {
		return true, nil
	}
	return false, nil
}
