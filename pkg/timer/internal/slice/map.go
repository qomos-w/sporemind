package slice

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

type OrderedMap[K comparable, V any] struct {
	keys   []K
	values map[K]V
}

// NewOrderedMap creates a new OrderedMap.
func NewOrderedMap[K comparable, V any]() *OrderedMap[K, V] {
	return &OrderedMap[K, V]{
		keys:   []K{},
		values: make(map[K]V),
	}
}

func CanConvertToType(i interface{}, t reflect.Type) bool {
	if i == nil {
		return false
	}
	val := reflect.ValueOf(i)
	if val.Type().ConvertibleTo(t) {
		return true
	}
	if t.Kind() == reflect.Interface && val.Type().Implements(t) {
		return true
	}
	return false
}

type IEnum interface {
	ToString() string
	FromString(s string) interface{}
}

func (this *OrderedMap[K, V]) KeyType() reflect.Type {
	return reflect.TypeFor[K]()
}

func (this *OrderedMap[K, V]) ValueType() reflect.Type {
	return reflect.TypeFor[V]()
}

func (this *OrderedMap[K, V]) SetAny(k any, v any) {
	key, ok := k.(K)
	if !ok {
		return
	}
	value, ok := v.(V)
	if !ok {
		return
	}
	this.Set(key, value)
}

func (this *OrderedMap[K, V]) KeysAny() []any {
	ret := make([]any, len(this.keys))
	for i, k := range this.keys {
		ret[i] = k
	}
	return ret
}

func (this *OrderedMap[K, V]) ValuesAny() []any {
	ret := make([]any, len(this.keys))
	for i, k := range this.keys {
		ret[i] = this.values[k]
	}
	return ret
}

func (this *OrderedMap[K, V]) GetAny(k any) any {
	if key, ok := k.(K); ok {
		return this.values[key]
	}
	return nil
}

func (this *OrderedMap[K, V]) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))

	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("expected { at start")
	}

	var keys []K
	var values []V

	// 临时存储每个字段对应原始JSON数据
	for dec.More() {
		tk, err := dec.Token()
		if err != nil {
			return err
		}
		keyStr := tk.(string)

		// 把值先读成 RawMessage（原始JSON）
		var raw json.RawMessage

		if err := dec.Decode(&raw); err != nil {
			return err
		}

		// 把key转成 K 类型（根据你的需求）
		var k K
		// 这里假设 K 是 string 类型，如果不是，你需要根据实际情况转换
		//假设K是整形
		if reflect.TypeOf(k).Kind() == reflect.Int {
			intValue, err := json.Number(keyStr).Int64()
			if err != nil {
				return fmt.Errorf("convert key %v to int failed: %w", keyStr, err)
			}
			k = any(intValue).(K) // 如果 K 是 int 可以这样
		} else if CanConvertToType(k, reflect.TypeFor[IEnum]()) {
			enumK := any(k).(IEnum)
			k = enumK.FromString(keyStr).(K)
		} else if reflect.TypeOf(k).Kind() == reflect.Int32 {
			rawV, err := json.Number(keyStr).Int64()
			if err != nil {
				return fmt.Errorf("convert key %v to int32 failed: %w", keyStr, err)
			}
			int32Value := int32(rawV)
			k = any(int32Value).(K)
		} else if reflect.TypeOf(k).Kind() == reflect.Int64 {
			int64Value, err := json.Number(keyStr).Int64()
			if err != nil {
				return fmt.Errorf("convert key %v to int64 failed: %w", keyStr, err)
			}
			k = any(int64Value).(K) // 如果 K 是 int64 可以这样
		} else if reflect.TypeOf(k).Kind() == reflect.String {
			k = any(keyStr).(K) // 如果 K 是 string 可以这样
		} else {
			return fmt.Errorf("unsupported key type: %T", k)
		}

		keys = append(keys, k)

		// 把 raw 再反序列化到目标值 V 类型中（需要你保证 V 可以正常 unmarshal）

		var v V
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("unmarshal value for key %v failed: %w", keyStr, err)
		}
		values = append(values, v)
	}

	tokEnd, _ := dec.Token()
	if delimEnd, ok := tokEnd.(json.Delim); !ok || delimEnd != '}' {
		return fmt.Errorf("expected } at the end")
	}

	this.keys = keys

	if this.values == nil {
		this.values = make(map[K]V)
	}
	for i := range keys {
		this.values[keys[i]] = values[i]
	}

	return nil
}

func (this *OrderedMap[K, V]) MarshalJSON() ([]byte, error) {
	str := "{"
	for i, k := range this.keys {
		v := this.values[k]
		if i > 0 {
			str += ","
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal key: %w", err)
		}
		value, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal value: %w", err)
		}
		str += fmt.Sprintf("%s:%s", key, value)
	}
	str += "}"
	return []byte(str), nil
}

func (this *OrderedMap[K, V]) ToMap() map[K]V {
	ret := make(map[K]V, len(this.keys))
	for _, k := range this.keys {
		ret[k] = this.values[k]
	}
	return ret
}

func (this *OrderedMap[K, V]) IsEmpty() bool {
	return len(this.keys) == 0
}

func (this *OrderedMap[K, V]) First() (K, V, bool) {
	if len(this.keys) == 0 {
		return Nil[K](), Nil[V](), false
	}
	k := this.keys[0]
	v := this.values[k]
	return k, v, true
}

func (this *OrderedMap[K, V]) FirstOrDefault() (K, V) {
	if len(this.keys) == 0 {
		return Nil[K](), Nil[V]()
	}
	k := this.keys[0]
	v := this.values[k]
	return k, v
}

func (this *OrderedMap[K, V]) FistKey() (K, bool) {
	if len(this.keys) == 0 {
		return Nil[K](), false
	}
	k := this.keys[0]
	return k, true
}

func (this *OrderedMap[K, V]) FistKeyOrDefault() K {
	if len(this.keys) == 0 {
		return Nil[K]()
	}
	k := this.keys[0]
	return k
}

func (this *OrderedMap[K, V]) FirstValue() (V, bool) {
	if len(this.keys) == 0 {
		return Nil[V](), false
	}
	k := this.keys[0]
	v := this.values[k]
	return v, true
}

func (this *OrderedMap[K, V]) FirstValueOrDefault() V {
	if len(this.keys) == 0 {
		return Nil[V]()
	}
	k := this.keys[0]
	v := this.values[k]
	return v
}

func (this *OrderedMap[K, V]) Last() (K, V, bool) {
	if len(this.keys) == 0 {
		return Nil[K](), Nil[V](), false
	}
	k := this.keys[len(this.keys)-1]
	v := this.values[k]
	return k, v, true
}

func (this *OrderedMap[K, V]) LastOrDefault() (K, V) {
	if len(this.keys) == 0 {
		return Nil[K](), Nil[V]()
	}
	k := this.keys[len(this.keys)-1]
	v := this.values[k]
	return k, v
}

func (this *OrderedMap[K, V]) LastKey() (K, bool) {
	if len(this.keys) == 0 {
		return Nil[K](), false
	}
	k := this.keys[len(this.keys)-1]
	return k, true
}

func (this *OrderedMap[K, V]) LastKeyOrDefault() K {
	if len(this.keys) == 0 {
		return Nil[K]()
	}
	k := this.keys[len(this.keys)-1]
	return k
}

func (this *OrderedMap[K, V]) LastValue() (V, bool) {
	if len(this.keys) == 0 {
		return Nil[V](), false
	}
	k := this.keys[len(this.keys)-1]
	v := this.values[k]
	return v, true
}

func (this *OrderedMap[K, V]) LastValueOrDefault() V {
	if len(this.keys) == 0 {
		return Nil[V]()
	}
	k := this.keys[len(this.keys)-1]
	v := this.values[k]
	return v
}

func (this *OrderedMap[K, V]) HasValue(v V) bool {
	for _, k := range this.keys {
		if reflect.DeepEqual(this.values[k], v) {
			return true
		}
	}
	return false
}

func (this *OrderedMap[K, V]) IndexOf(k K) int {
	for i, key := range this.keys {
		if key == k {
			return i
		}
	}
	return -1
}

// IndexOfValue returns the index of the first occurrence of v in the map.
func (this *OrderedMap[K, V]) IndexOfValue(v V) int {
	for i, k := range this.keys {
		if reflect.DeepEqual(this.values[k], v) {
			return i
		}
	}
	return -1
}

// Has checks if the map contains the given k.
func (this *OrderedMap[K, V]) Has(k K) bool {
	_, exists := this.values[k]
	return exists
}

// Set adds or updates a k-v pair in the map.
func (this *OrderedMap[K, V]) Set(k K, v V) {
	this.keys = AppendOnce(this.keys, k)
	this.values[k] = v
}

// Get retrieves the v associated with the given k.
func (this *OrderedMap[K, V]) Get(k K) V {
	v := this.values[k]
	return v
}

func (this *OrderedMap[K, V]) GetValue(k K) (V, bool) {
	v, ok := this.values[k]
	if !ok {
		return Nil[V](), false
	}
	return v, true
}

func (this *OrderedMap[K, V]) GetKeyByValue(v V) (K, bool) {
	for _, k := range this.keys {
		if reflect.DeepEqual(this.values[k], v) {
			return k, true
		}
	}
	return Nil[K](), false
}

func (this *OrderedMap[K, V]) SetKV(kv ...KV[K, V]) {
	for _, item := range kv {
		this.Set(item.K, item.V)
	}
}

func (this *OrderedMap[K, V]) GetKV(k K) KV[K, V] {
	v, exists := this.values[k]
	if !exists {
		return KV[K, V]{K: k, V: Nil[V]()}
	}
	return KV[K, V]{K: k, V: v}
}

// Delete removes a k-v pair from the map.
func (this *OrderedMap[K, V]) Delete(k K) V {
	if v, exists := this.values[k]; exists {
		delete(this.values, k)
		for i, ks := range this.keys {
			if k == ks {
				this.keys = append(this.keys[:i], this.keys[i+1:]...)
				break
			}
		}
		return v
	}
	return Nil[V]()
}

// Keys returns the keys in insertion order.
func (this *OrderedMap[K, V]) Keys() []K {
	return this.keys
}

// Values returns the values in insertion order.
func (this *OrderedMap[K, V]) Values() []V {
	vs := make([]V, len(this.keys))
	for i, k := range this.keys {
		vs[i] = this.values[k]
	}
	return vs
}

// SortByKey reorders the keys in the map based on the provided comparison function.
func (this *OrderedMap[K, V]) SortByKey(less func(a, b K) bool) {
	sort.Slice(this.keys, func(i, j int) bool {
		return less(this.keys[i], this.keys[j])
	})
}

// SortByValue reorders the keys in the map based on the values using the provided comparison function.
func (this *OrderedMap[K, V]) SortByValue(less func(a, b V) bool) {
	sort.Slice(this.keys, func(i, j int) bool {
		return less(this.values[this.keys[i]], this.values[this.keys[j]])
	})
}

func (this *OrderedMap[K, V]) Sort(less func(a, b KV[K, V]) bool) {
	sort.Slice(this.keys, func(i, j int) bool {
		return less(KV[K, V]{K: this.keys[i], V: this.values[this.keys[i]]}, KV[K, V]{K: this.keys[j], V: this.values[this.keys[j]]})
	})
}

// RangeFunc iterates over the k-v pairs in insertion order and applies the provided function.
// If the function returns false, the iteration stops.
func (this *OrderedMap[K, V]) Range(f func(i int, k K, v V) bool) {
	for i, k := range this.keys {
		if !f(i, k, this.values[k]) {
			break
		}
	}
}

func (this *OrderedMap[K, V]) ForEach(f func(k K, v V)) {
	for _, k := range this.keys {
		f(k, this.values[k])
	}
}

func (this *OrderedMap[K, V]) RangeReverse(f func(i int, k K, v V) bool) {
	for i := len(this.keys) - 1; i >= 0; i-- {
		k := this.keys[i]
		if !f(i, k, this.values[k]) {
			break
		}
	}
}

func (this *OrderedMap[K, V]) SetAtIdx(idx int, k K, v V) {
	if idx < 0 || idx >= len(this.keys) {
		return
	}
	if this.keys[idx] != k {
		this.Delete(k)
		this.keys[idx] = k
	}
	this.values[k] = v
}

func (this *OrderedMap[K, V]) GetAtIdx(idx int) (K, V, bool) {
	if idx < 0 || idx >= len(this.keys) {
		return Nil[K](), Nil[V](), false
	}
	k := this.keys[idx]
	v, exists := this.values[k]
	if !exists {
		return k, Nil[V](), false
	}
	return k, v, true
}

func (this *OrderedMap[K, V]) GetValueAtIdx(idx int) V {
	if idx < 0 || idx >= len(this.keys) {
		return Nil[V]()
	}
	k := this.keys[idx]
	v, exists := this.values[k]
	if !exists {
		return Nil[V]()
	}
	return v
}

func (this *OrderedMap[K, V]) InsertAtIdx(idx int, k K, v V) {
	if idx < 0 || idx > len(this.keys) {
		return
	}
	if this.Has(k) {
		this.SetAtIdx(idx, k, v)
		return
	}
	this.keys = append(this.keys[:idx], append([]K{k}, this.keys[idx:]...)...)
	this.values[k] = v
}

func (this *OrderedMap[K, V]) RemoveAtIdx(idx int) {
	if idx < 0 || idx >= len(this.keys) {
		return
	}
	k := this.keys[idx]
	delete(this.values, k)
	this.keys = append(this.keys[:idx], this.keys[idx+1:]...)
}

func (this *OrderedMap[K, V]) Clear() {
	this.Reset()
}

func (this *OrderedMap[K, V]) Reset() {
	this.keys = []K{}
	this.values = make(map[K]V)
}

func (this *OrderedMap[K, V]) Len() int {
	return len(this.keys)
}

func (this *OrderedMap[K, V]) Swap(i, j int) {
	if i < 0 || i >= len(this.keys) || j < 0 || j >= len(this.keys) {
		return
	}
	this.keys[i], this.keys[j] = this.keys[j], this.keys[i]
	this.values[this.keys[i]], this.values[this.keys[j]] = this.values[this.keys[j]], this.values[this.keys[i]]
}

func (this *OrderedMap[K, V]) Pop() (K, V, bool) {
	if len(this.keys) == 0 {
		return Nil[K](), Nil[V](), false
	}
	k := this.keys[len(this.keys)-1]
	v := this.values[k]
	delete(this.values, k)
	this.keys = this.keys[:len(this.keys)-1]
	return k, v, true
}

func (this *OrderedMap[K, V]) PopFront() (K, V, bool) {
	if len(this.keys) == 0 {
		return Nil[K](), Nil[V](), false
	}
	k := this.keys[0]
	v := this.values[k]
	delete(this.values, k)
	this.keys = this.keys[1:]
	return k, v, true
}

func (this *OrderedMap[K, V]) Clone() *OrderedMap[K, V] {
	ret := NewOrderedMap[K, V]()
	for _, k := range this.keys {
		ret.Set(k, this.values[k])
	}
	return ret
}

func (this *OrderedMap[K, V]) Push(k K, v V) {
	if this.Has(k) {
		this.Set(k, v)
		return
	}
	this.keys = append(this.keys, k)
	this.values[k] = v
}

func ContainKeys[K comparable, V any](m map[K]V, keys ...K) bool {
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}
