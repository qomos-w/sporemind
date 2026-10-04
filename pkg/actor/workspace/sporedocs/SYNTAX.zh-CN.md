# sporescript 语言参考

本文档是 sporescript 语言参考：词法结构、类型、声明、语句、表达式、闭包、流式 callable 与宿主互操作。

[English](SYNTAX.md)

sporescript 是一门静态类型语言，编译到基于栈的字节码并在嵌入式 VM 上运行。它为宿主嵌入而设计：每个声明都映射到 schema 描述符、callable 描述符或宿主绑定，每个值形状都可投影到传输层。

---

## 1. 词法结构

- 标识符：字母、数字、`_`；不能以数字开头。
- 注释：行注释 `//` 与块注释 `/* ... */`。
- 数字字面量仅十进制。**没有数字后缀**（不存在 `1L`、`1.5f`）；宽度由声明处的目标类型决定（见 §7）。
- 字符串字面量用双引号；`bytes` 是独立类型。
- 关键字保留，不能作标识符。

### 关键字

```
fun export class struct enum type var package import
void any bool byte short ushort uint long ulong double bytes
array map int float string media
return if else for while in when case break continue
is as from this new true false null super constructor
open override interface public private static
stream yield async await optional key try catch defer
```

## 2. 类型

### 2.1 标量与特殊类型

| 类型 | 含义 |
|------|------|
| `bool` | 布尔 |
| `byte` `short` `ushort` `int` `uint` `long` `ulong` | 定宽整数（8/16/32/64 位，有符号/无符号） |
| `float` `double` | IEEE-754 32/64 位 |
| `string` | UTF-8 字符串 |
| `bytes` | 字节序列 |
| `any` | 类型擦除值 |
| `void` | 无值（仅返回位） |
| `null` | null 字面量 / 可空缺席 |
| `media` | 宿主媒体句柄 |

### 2.2 数组与有序 map

```spore
var xs: int[] = [1, 2, 3]
var m: map<string, int> = {"a": 1, "b": 2}
```

`array<T>` 是 `T[]` 的长形式。map 是**有序 map** 语义面：查找 `m["key"]`、赋值 `m["key"] = v`、`delete(m, "key")`、`len(m)`、枚举、快照/diff 都观察到稳定条目顺序。map 字面量的键是 string。

### 2.3 struct —— 值语义面

`struct` 是对 schema 友好的值形状：命名字段、值传递、易于描述、diff 与传输。

```spore
struct Point {
    x: int
    y: int
}

var p: Point = Point{x: 10, y: 20}
```

**struct 字面量要求全字段。** 想让剩余字段取类型零值，写尾部 `..` —— 显式选择加入：

```spore
var p: Point = Point{..}            // x=0, y=0
var q: Point = Point{x: 7, ..}      // x=7, y=0
```

`..` 至多出现一次且只能在末尾。不写 `..` 而缺字段是编译错误 `missing_struct_field`。

**声明装饰器：**

```spore
@schema(3)
@component
struct Event {
    id: string
    optional payload: string
}
```

- `@schema(N)` —— 声明传输层 schema id（单个整数字面量，仅用于 `struct` 声明）。进入 `ObjectDesc.SchemaID`。
- `@component` —— 把 struct 标记为 ECS 组件（`ObjectDesc.IsComponent`）。组件必须携带 schema id；携带 schema id 的 struct 不自动成为组件。可与 `@schema(N)` 任意顺序组合。
- `optional` —— `struct` 与 `class` 字段的修饰符，在描述符中标记字段可选（`FieldDesc.Optional`）。它是 schema 元数据，**不**放宽字面量完整性规则。字段名恰好叫 `optional`（`optional: T`）仍按字段名解析。

### 2.3.1 `@data` —— 键控数据表行

`@data` 声明一个其实例为键控数据表行（启动期加载的配置数据）的 struct，而非 ECS 组件。它是与普通 schema struct、`@component` 平行的第三种 struct 角色：

```spore
@data
@version(2)
struct AnimalType {
    key id: string
    name: string
    biome: BiomeKind
    @ref(AnimalType) optional evolvesInto: string
}
```

- `@data` —— 无参数。该 struct 永远不携带传输 schema id：与 `@schema(N)` 或 `@component` 组合均为错误。代码生成器输出纯 struct——无组件描述符、不进 world registry——并按需附带数据表清单。
- `@version(N)` —— 可选，仅 `@data` struct。面向消费方的格式版本号，用于加载数据的前向兼容门禁（`ObjectDesc.DataVersion`，0 = 未版本化）。与传输 schema id 无关。
- `key` —— 字段修饰符（保留字与 `key: T` 消歧规则同 `optional`），标记表的键字段。每个 `@data` struct 恰好一个；不可为 optional；类型必须是 `string`、整数标量（`byte short ushort int uint long ulong`）或枚举。非 `@data` struct 中出现 `key`/`@ref` 标记为错误。
- `@ref(T)` / `@ref(T.field)` —— 字段装饰器，声明指向 `@data` struct `T` 的外键；`@ref(T)` 指向 T 的键字段，`@ref(T.field)` 指向 T 的显式字段。字段必须是 `string` 或 `array<string>`（数组逐元素引用），且 T 必须为 string 键。可与 `optional` 组合（可空引用）；装饰器在修饰符之前（`@ref(T) optional boss: string`）。跨 struct 规则在代码生成期强制执行；解析器本身只检查装饰器拼写与参数形态。

### 2.4 class —— 对象语义面

`class` 定义带字段、方法、构造器、继承与重写分派的对象：

```spore
class User {
    id: string
    name: string

    fun displayName(): string {
        return name
    }
}
```

已实现的 class 机制：`this`、`new`、`constructor`、`super.method()`、`super()` 构造链、`open`、`override`、继承（`class Person : Greeter`）。class 字段支持与 struct 相同的 `optional` 修饰符。

struct 是首选的**导出安全载体**；class 是对象与绑定面。导出的 struct 图不可达 class 类型——此类导出会被拒绝。

### 2.5 interface

接口声明方法签名，可携带**默认实现体**：

```spore
interface Greeter {
    fun greet(): string { return "hello" }   // 默认实现
    fun name(): string                        // 抽象：class 必须提供
}

class Person : Greeter {
    fun name(): string { return "alice" }
    // greet() 继承自接口默认实现
}

class Robot : Greeter {
    fun greet(): string { return "beep" }     // 覆盖默认实现
    fun name(): string { return "r2" }
}
```

默认实现体遵循普通方法体规则，并对 `this` 动态分派，因此默认实现可以调用实现类提供的其他接口方法。抽象接口方法既未被类提供、也无继承实现时，报 `interface_method_missing`。

### 2.6 类型别名

```spore
type UserID = string
```

别名为 schema 契约命名；不引入新的运行时表示。

### 2.7 函数类型

类型位上的 `fun(P1, P2): R`。函数类型闭括号 `)` 之后的返回类型属于该函数类型；callable 自身的返回注解跟在自己的参数表之后，两者不冲突：

```spore
fun apply(cb: fun(int): int): int {
    return cb(1)
}
```

## 3. 声明

### 3.1 `fun`、`export fun`

```spore
fun add(a: int, b: int): int {
    return a + b
}

export fun greet(name: string): string {
    return "hello, " + name
}
```

`export fun` 声明**可以**参与外部绑定的 callable；实际暴露只发生在宿主侧显式的绑定/注册步骤之后。

### 3.2 `stream fun` —— 流式 callable

```spore
stream fun chat(prompt: string): MessageStreamEvent {
    yield MessageStreamEvent{kind: "delta"}
    return MessageStreamEvent{kind: "end"}
}
```

完整流式契约见 §8。

### 3.3 `var`

```spore
var count: int = 42
```

函数体内 `var` 是普通局部绑定。顶层 `var` 声明全局配置/状态；加载模块不会执行任意顶层逻辑。

### 3.4 `package`

模块用 `package` 头声明名字。模块链接语义是显式 import/export（§9）。

## 4. 语句与控制流

| 语句 | 形式 |
|------|------|
| `if / else` | `if cond { ... } else { ... }` |
| `when / case` | 见下 |
| `for in` | `for x in xs { ... }` |
| `while` | `while cond { ... }` |
| `return` | `return expr` / `return` |
| `break` / `continue` | 循环控制 |
| `try / catch` | `try { ... } catch (e) { ... }` |
| `defer` | `defer { ... }` |
| `yield` | 仅流式 callable（§8） |

`when / case` 支持值匹配、带绑定的类型匹配、guard 与可选 `else` 分支：

```spore
when classify(v) {
    case 1, 2, 3 { return "low" }
    case x: int when x > 100 { return "big" }
    case s: string { return "text" }
    else { return "other" }
}
```

`case ... when guard` 的 guard 为假时落入下一个 case（或 `else`）。

## 5. 表达式

| 类别 | 运算符 / 形式 |
|------|---------------|
| 成员访问 | `.` |
| 可选链 | `x?.field`、`x?.method(args)` —— null 接收者把整条链短路为 `null` |
| null 合并 | `x ?? y` —— 仅 `x` 为 null 时取 `y`（false/0/"" 不是 null）；左结合，比 `\|\|` 结合更松 |
| 调用 | `f(args)` |
| 索引 | `a[i]`、`m["key"]` |
| 算术 | `+ - * / %` |
| 比较 | `== != < <= > >=` |
| 逻辑 | `&& \|\| !` |
| 类型检查/转换 | `is`、`as` |

`as` 转换失败产生可诊断错误 `type_cast_failed`；`is` 作用于非对象操作数安全返回 `false`——两者都不会 panic。

## 6. lambda 与闭包

lambda 是表达式位置上的匿名函数，参数/返回语法与 `fun` 相同：

```spore
var f = fun(x: int): int { return x * 2 }
var g = fun(x: int): int = x * 2          // 表达式体
```

箭头短形式（仅单表达式体）：

```spore
var dbl: any = (x): int => x * 2        // 带括号参数，带类型
var inc: any = x => x + 1                // 单个无类型参数，无括号
var add: any = (a, b) => a + b           // 多个无类型参数
var mk: any  = (): int => 42             // 零参数
```

规则：

- 带括号形式：`(p: T?, ...): R? => expr`；注解可省略；允许函数类型（`(cb: fun(int): int): int => cb(1)`）。
- `p => expr` 无括号、无注解。
- 体只能是一个表达式；块体用 `fun` 形式。
- `=>` 与 `->`、`>=`、`==` 是不同 token。

闭包语义：

- 捕获变量与外围作用域**按引用共享**——lambda 内的赋值对外可见，反之亦然。
- `for in` 循环变量**每次迭代独立绑定**：不同迭代中创建的闭包观察各自的值。C 风格 `for` 变量是单一共享绑定。
- 捕获声明之前的变量是编译错误。
- lambda 赋给函数类型槽位时**编译期检查**：参数个数、参数类型、声明的返回类型必须匹配。留为 `any`/未注解的分量对该分量跳过检查。

## 7. 数字字面量语义

语言没有数字后缀。整数字面量解析为 `int64`、浮点解析为 `float64`；**声明处的目标类型**（type hint）决定字面量编码：

| 声明 | 编码 |
|---|---|
| `var x: int = 1` | 32 位 int |
| `var x: long = 1` | 64 位 int |
| `var x: double = 1.0` | 64 位浮点 |
| `var d = 1.5`（无注解） | 32 位浮点 |

type hint 生效于声明初始化、`return`（按声明返回类型）、lambda/表达式体、struct 字面量字段值——**不**作用于普通 `=` 再赋值。超出 32 位范围的值请显式注解 `long` / `double`。

## 8. 流式 callable（`stream fun`）

- `stream fun name(...): T` 声明单一脚本可见条目类型 `T` 的流式 callable。
- `yield expr` 仅在 `stream fun` 内合法；发射一个中间条目，要求 `expr: T`。绑定层降级为 `next`。
- `return expr` 发射终止条目（降级为 `final`）并立即耗尽流会话。它可以是首个被外部观察到的发射。
- 成功 `final` 之后再 `next`/`final` 属于契约外行为，表现为结构化流耗尽。
- `yield` 不允许出现在 `try` 块或 `defer` 体内（`yield_disallowed_in_try` / `yield_in_defer`）：挂起无法保留 catch/defer 上下文。`catch` 体内的 `yield` 允许——handler 在 catch 进入时即被消费。
- 「跳过坏条目」循环：把易错段移进 try/catch 不跨越挂起的辅助函数，再 yield 其结果：

```spore
fun safeStep(i: int): int {
    try { return risky(i) } catch (e) { return 0 }
}

stream fun pipeline(items: int[]): int {
    for i in items { yield safeStep(i) }
    return 0
}
```

- 消息式流的约定 `T = MessageStreamEvent`，`kind` 区分 `start` / `delta` / `end`。

## 9. 模块与宿主互操作

import 形式：

```spore
import add from "math"                      // 单符号
import add as plus from "math"              // 单符号 + 本地别名
import { add, version } from "math"         // 命名列表
import { add as plus, version as v } from "math"  // 命名列表 + 别名
export add from "math"                      // 再导出
```

- 同一语法覆盖脚本模块与**原生宿主模块**——Go 侧 `rt.BindFunc("math", "add", ...)` 注册的函数以 `import { add } from "math"` 导入。
- `export struct` 与 `export type` 是编译期导出：导入方看到类型名与描述符形状，不是运行时句柄。
- **编译期**类型导入允许环（互相 `export struct`/`export type`）。**运行时**符号导入环被拒绝，报 `cyclic_import`。

## 10. 诊断索引

| 诊断 | 含义 |
|---|---|
| `missing_struct_field` | struct 字面量缺字段且未写 `..` |
| `type_cast_failed` | `as` 转换失败 |
| `cyclic_import` | 运行时符号导入环 |
| `yield_disallowed_in_try` | `try` 块内 `yield` |
| `yield_in_defer` | `defer` 体内 `yield` |
| `interface_method_missing` | 抽象接口方法未实现 |
