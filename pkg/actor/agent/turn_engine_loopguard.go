package agent

import (
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// ── 文本/思维链重复检测（loop guard）──
//
// 同一 turn 内 dispatch 迭代可能死循环：模型每轮产出几乎相同的
// reasoning + 文本并重复发起相同操作。此 guard 在 finalizeDispatch 后
// 对每轮产出的 (ReasoningContent + 文本) 做近重复检测：
//  1. MinHash sketch（64 维，rune 5-gram shingle）近似 Jaccard —— 常驻，
//     每轮 O(文本长度)，比较 O(64)；
//  2. sketch 触发后用 flate 压缩比确认 —— 两段文本拼接后仍高度可压缩
//     说明存在大块逐字重复，排除 sketch 的模板/套话误报。
//
// 检测命中先注入随机化的纠正 user 消息（走 pendingMessages，下一次
// dispatch 在 tool_result 之后被消费，协议安全）；连续命中达到上限则
// 硬断 turn（failTurn），由既有收尾路径处理孤儿 tool_use。

const (
	// loopGuardWindow 参与比较的历史迭代数（环形窗口）。
	loopGuardWindow = 6
	// loopGuardShingleLen 是 shingle 的 rune n-gram 长度。
	loopGuardShingleLen = 5
	// loopGuardMinRunes 忽略过短的产出，避免短句/套话误报。
	loopGuardMinRunes = 64
	// loopGuardSimThreshold sketch 相似度阈值，高于视为近重复候选。
	loopGuardSimThreshold = 0.68
	// loopGuardConfirmRatio flate 联合压缩比阈值（C(a+b)/(C(a)+C(b))）：
	// 无关文本≈1.0，逐字重复≈0.5；低于阈值才确认重复。
	loopGuardConfirmRatio = 0.75
	// loopGuardMaxStrikes 连续命中次数上限：前两次注入纠正，第三次硬断。
	loopGuardMaxStrikes = 3
	// loopGuardTextCap 每条记录保存的规范化文本上限（flate 输入边界）。
	loopGuardTextCap = 16 << 10
)

// loopSketch 是一段文本的 64 维 MinHash sketch（128 * 8 字节内）。
type loopSketch [64]uint64

// loopGuardEntry 记录一次 dispatch 迭代的检测材料。
type loopGuardEntry struct {
	sketch loopSketch
	text   string // 规范化文本，截断到 loopGuardTextCap
}

// loopGuardNormalize 折叠空白并小写化，保留 CJK 字符。
func loopGuardNormalize(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// fnv1aRune 对一个 rune 切片做 FNV-1a 64 位哈希。
func fnv1aRune(rs []rune) uint64 {
	const prime = 1099511628211
	h := uint64(14695981039346656037)
	for _, r := range rs {
		for i := 0; i < 8; i++ {
			h ^= uint64(byte(r))
			h *= prime
			r >>= 8
		}
	}
	return h
}

// loopMixA/B 是 64 组 MinHash 置换常数（初始化后不可变）。
// A 组乘法常数强制为奇数，满足 2-universal 哈希要求。
var (
	loopMixA = buildLoopMix(0x9e3779b97f4a7c15, true)
	loopMixB = buildLoopMix(0xbf58476d1ce4e5b9, false)
)

func buildLoopMix(seed uint64, forceOdd bool) [64]uint64 {
	var out [64]uint64
	for i := 0; i < 64; i++ {
		x := seed * uint64(2*i+1)
		x ^= x >> 30
		x *= 0xbf58476d1ce4e5b9
		x ^= x >> 27
		x *= 0x94d049bb133111eb
		x ^= x >> 31
		if forceOdd {
			x |= 1
		}
		out[i] = x
	}
	return out
}

// sketchLoopText 对规范化文本的 rune n-gram 集合做 64 维 MinHash。
func sketchLoopText(s string) loopSketch {
	rs := []rune(s)
	var sk loopSketch
	for i := range sk {
		sk[i] = ^uint64(0)
	}
	for i := 0; i+loopGuardShingleLen <= len(rs); i++ {
		h := fnv1aRune(rs[i : i+loopGuardShingleLen])
		for j := range sk {
			v := h*loopMixA[j] + loopMixB[j]
			if v < sk[j] {
				sk[j] = v
			}
		}
	}
	return sk
}

// sketchSimilarity 估计两段文本 shingle 集合的 Jaccard 相似度。
func sketchSimilarity(a, b loopSketch) float64 {
	eq := 0
	for i := range a {
		if a[i] == b[i] {
			eq++
		}
	}
	return float64(eq) / float64(len(a))
}

// flateJointRatio 返回联合压缩比 C(a+b)/(C(a)+C(b))。
// 分子是两段拼接后的压缩长度；分母是各自单独压缩之和。
// 两段无关时≈1.0（各自独立压缩），逐字重复时≈0.5（共享全部内容）。
func flateJointRatio(a, b string) float64 {
	ca := flateLen(a)
	cb := flateLen(b)
	if ca+cb == 0 {
		return 1
	}
	return float64(flateLen(a+"\x00"+b)) / float64(ca+cb)
}

func flateLen(s string) int {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return len(s)
	}
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return buf.Len()
}

// detectTextLoop 判断 normText 是否与窗口内某历史迭代近重复。
// 返回 (确认重复, 最佳相似度)。
func detectTextLoop(normText string, prev []loopGuardEntry) (bool, float64) {
	if len(prev) == 0 {
		return false, 0
	}
	sk := sketchLoopText(normText)
	best := 0.0
	bestIdx := -1
	for i := range prev {
		if s := sketchSimilarity(sk, prev[i].sketch); s > best {
			best = s
			bestIdx = i
		}
	}
	if best < loopGuardSimThreshold || bestIdx < 0 {
		return false, best
	}
	if flateJointRatio(normText, prev[bestIdx].text) > loopGuardConfirmRatio {
		return false, best
	}
	return true, best
}

// loopCorrectionPool 是纠正消息的措辞变体池。随机选取：重复的纠正
// 提示本身就是一种重复输入，会让模型以同样的方式无视它。
var loopCorrectionPool = []string{
	"Repetition detected: this output is nearly identical to an earlier step in this turn. Do not produce it again. Re-read the most recent tool results, state in one line why the previous approach did not advance the goal, and take a materially different action.",
	"Loop guard: you just repeated an earlier step verbatim. Stop. Identify the first assumption that failed, drop it, and choose a different tool or a different target this time.",
	"Loop detected: same reasoning, same output. Repeating it will not change the result. Break the cycle: pick the smallest concrete action you have NOT tried yet and execute it.",
	"You are stuck in a loop — this iteration matches a previous one almost exactly. Acknowledge the blocker explicitly, then work around it instead of retrying the same path.",
	"Repetition guard: the previous iterations already covered this ground. Summarize what you have tried in one short list, then select an approach that is not on it.",
	"Duplicate output detected. Continuing the identical pattern wastes the turn budget. Change strategy now: verify state with a read-only check, then act on what differs from your assumption.",
}

// loopCorrectionText 返回第 idx（对池长取模）条纠正消息。
func loopCorrectionText(idx int) string {
	return loopCorrectionPool[idx%len(loopCorrectionPool)]
}

// appendLoopGuardEntry 维护环形窗口：追加并截断到 loopGuardWindow。
func appendLoopGuardEntry(ring []loopGuardEntry, normText string) []loopGuardEntry {
	t := normText
	if len(t) > loopGuardTextCap {
		t = t[:loopGuardTextCap]
	}
	ring = append(ring, loopGuardEntry{sketch: sketchLoopText(normText), text: t})
	if len(ring) > loopGuardWindow {
		ring = ring[len(ring)-loopGuardWindow:]
	}
	return ring
}

// runLoopGuard 检查一次 dispatch 迭代产出的 reasoning+文本是否与近期
// 迭代近重复。命中时：未达上限 → 注入随机化纠正消息（软纠正，走
// pendingMessages 在下一次 dispatch 消费）；达到上限 → failTurn 硬断。
// 返回 true 表示 turn 已被判定失败（调用方应立即 return）。
// 只在 agent.exec 循环上调用（与 loopState 相同的单写者模型）。
func (e *turnEngine) runLoopGuard(ctx actor.Context, turnID, reasoning, text string) bool {
	combined := strings.TrimSpace(loopGuardNormalize(reasoning) + " " + loopGuardNormalize(text))
	if utf8.RuneCountInString(combined) < loopGuardMinRunes {
		// 产出过短，无法可靠判断；不记录，避免污染窗口。
		return false
	}
	matched, sim := detectTextLoop(combined, e.loopGuardRing)
	e.loopGuardRing = appendLoopGuardEntry(e.loopGuardRing, combined)
	if !matched {
		e.loopGuardStrikes = 0
		return false
	}
	e.loopGuardStrikes++
	if e.loopGuardStrikes >= loopGuardMaxStrikes {
		msg := fmt.Sprintf("Repetition loop guard: iteration output near-identical to an earlier iteration (similarity %.2f); turn interrupted after %d ignored corrections.", sim, e.loopGuardStrikes-1)
		e.logger.Warn("turnEngine: loop guard hard break", "turnID", turnID, "similarity", sim, "strikes", e.loopGuardStrikes)
		e.reportDiagnosticIfSet(ctx, "loop-guard", msg, turnID)
		e.failTurn(ctx, turnID, errors.New(msg))
		return true
	}
	correction := loopCorrectionText(rand.IntN(len(loopCorrectionPool)))
	e.logger.Warn("turnEngine: repetition detected, queuing correction", "turnID", turnID, "similarity", sim, "strike", e.loopGuardStrikes)
	e.appendMessages([]domain.ChatMessage{{
		Role: domain.ChatRoleUser,
		Content: []domain.ContentBlock{{
			Type: domain.ContentBlockText,
			Text: correction,
		}},
	}})
	return false
}
