package timer

import (
	"unsafe"
)

type Head struct {
	Next *Head
	Prev *Head
	len  int
}

// 初始化表头的函数，指向自己形成一个环
func (h *Head) Init() {
	h.Next = h
	h.Prev = h
	h.len = 0
}

func (h *Head) Add(new *Head) {
	h.len++
	add(new, h, h.Next)
}

func (h *Head) AddTail(new *Head) {
	h.len++
	add(new, h.Prev, h)
}

func (h *Head) Del(head *Head) {
	h.len--
	del(head.Prev, head.Next)
}

func (pos *Head) Entry(offset uintptr) unsafe.Pointer {
	return unsafe.Pointer((uintptr(unsafe.Pointer(pos)) - offset))
}

func (h *Head) FirstEntry(offset uintptr) unsafe.Pointer {
	return h.NextEntry(offset)
}

func (h *Head) LastEntry(offset uintptr) unsafe.Pointer {
	return h.PrevEntry(offset)
}

func (h *Head) FirstEntryOrNil(offset uintptr) unsafe.Pointer {
	if h.len == 0 {
		return nil
	}

	return h.FirstEntry(offset)
}

func (pos *Head) NextEntry(offset uintptr) unsafe.Pointer {
	return unsafe.Pointer((uintptr(unsafe.Pointer(pos.Next)) - offset))
}

func (pos *Head) PrevEntry(offset uintptr) unsafe.Pointer {
	return unsafe.Pointer((uintptr(unsafe.Pointer(pos.Prev)) - offset))
}

func (h *Head) ForEach(callback func(pos *Head)) {

	for pos := h.Next; pos != h; pos = pos.Next {
		callback(pos)
	}
}

func (h *Head) ForEachPrev(callback func(pos *Head)) {
	for pos := h.Prev; pos != h; pos = pos.Prev {
		callback(pos)
	}
}

func (h *Head) ForEachSafe(callback func(pos *Head)) {

	pos := h.Next
	n := pos.Next

	for pos != h {
		callback(pos)
		pos = n
		n = pos.Next
	}
}

func (h *Head) ForEachPrevSafe(callback func(pos *Head)) {
	pos := h.Prev
	n := pos.Prev
	for pos != h {
		callback(pos)
		pos = n
		n = pos.Prev
	}
}

func (h *Head) Len() int {
	return h.len
}

func (h *Head) Replace(new *Head) {
	old := h
	new.Next = old.Next
	new.Next.Prev = new
	new.Prev = old.Prev
	new.Prev.Next = new
	new.len = old.len
}

func (h *Head) ReplaceInit(new *Head) {
	h.Replace(new)
	h.Init()
}

func (h *Head) DelInit(pos *Head) {
	h.len--
	delEntry(pos)
	pos.Init()
}

func (h *Head) Move(list *Head) {
	delEntry(list)
	h.Add(list)
}

func (h *Head) MoveTail(list *Head) {
	delEntry(list)
	h.AddTail(list)
}

func (h *Head) IsLast() bool {
	return h.Next == h
}

func (h *Head) Empty() bool {
	//return h.Next == h
	return h.len == 0
}

func (h *Head) RotateLeft() {
	var first *Head
	if !h.Empty() {
		first = h.Next
		h.MoveTail(first)
	}
}

func del(prev *Head, next *Head) {
	next.Prev = prev
	prev.Next = next
}

func add(new, prev, next *Head) {
	next.Prev = new
	new.Next = next
	new.Prev = prev
	prev.Next = new
}

func delEntry(entry *Head) {
	del(entry.Prev, entry.Next)
}
