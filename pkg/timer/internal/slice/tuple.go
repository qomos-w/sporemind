package slice

type Tuple2[T1 any, T2 any] struct {
	V1 T1
	V2 T2
}

func NewTuple2[T1 any, T2 any](v1 T1, v2 T2) Tuple2[T1, T2] {
	return Tuple2[T1, T2]{V1: v1, V2: v2}
}

type Tuple3[T1 any, T2 any, T3 any] struct {
	V1 T1
	V2 T2
	V3 T3
}

func NewTuple3[T1 any, T2 any, T3 any](v1 T1, v2 T2, v3 T3) Tuple3[T1, T2, T3] {
	return Tuple3[T1, T2, T3]{V1: v1, V2: v2, V3: v3}
}

type Tuple4[T1 any, T2 any, T3 any, T4 any] struct {
	V1 T1
	V2 T2
	V3 T3
	V4 T4
}

func NewTuple4[T1 any, T2 any, T3 any, T4 any](v1 T1, v2 T2, v3 T3, v4 T4) Tuple4[T1, T2, T3, T4] {
	return Tuple4[T1, T2, T3, T4]{V1: v1, V2: v2, V3: v3, V4: v4}
}

type Tuple5[T1 any, T2 any, T3 any, T4 any, T5 any] struct {
	V1 T1
	V2 T2
	V3 T3
	V4 T4
	V5 T5
}

func NewTuple5[T1 any, T2 any, T3 any, T4 any, T5 any](v1 T1, v2 T2, v3 T3, v4 T4, v5 T5) Tuple5[T1, T2, T3, T4, T5] {
	return Tuple5[T1, T2, T3, T4, T5]{V1: v1, V2: v2, V3: v3, V4: v4, V5: v5}
}
