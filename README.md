# Motion Sequencer

`motion-sequencer-go` 是一个仅运行在本地进程内的确定性动效时间线编排与混合引擎。时间线、轨道、片段、关键帧和采样结果都在内存中表示；采样函数不推进状态，也不依赖系统时间、外部服务、goroutine 调度或调用顺序。

## 核心模型

- `Timeline`：按 `Channel` 分组的完整动效定义。
- `Channel`：一个输出通道，叠加多个 `Track`。
- `Track`：带非负 `Weight` 的轨道，包含若干 `Clip`。
- `Clip`：时间轴上的半开区间 `[Start, Start+Duration)`，带 `TransitionIn`、默认缓动和本地关键帧。
- `Keyframe`：片段本地偏移、标量值和可选缓动。
- `PlaybackState`：默认播放光标的已提交位置、速率、播放标记、单调提交序号和可选幂等令牌。
- `Cursor`：采样方独立拥有的轻量只读游标。

## 确定性采样语义

对同一个已发布的时间线，`SampleChannel(channel, t)` 是纯函数：

1. 结果只由时间线内容和绝对时间 `t` 决定。
2. 不修改任何状态；任意采样频率、任意调用顺序、重复调用相同时间都得到相同的 `float64` 结果。
3. 正向逐步 `Tick` 到某个位置与新引擎直接 `Seek` 到该位置采样相同。逐步推进应使用二进制上可精确重复的步长，或在业务上使用容差比较。
4. 时间非负且必须有限；`NaN` 与无穷会被拒绝。
5. 片段和轨道规范化为深拷贝后保存，调用方不能在发布后绕过锁修改内部数据。

关键帧缓动定义在区间起点关键帧上。查找顺序为：起点 `Keyframe.Easing` → `Clip.Easing` → `Track.Easing` → 线性。内置值：

- `linear`
- `in-quad`
- `out-quad`
- `in-out-cubic`

## 叠加与权重

同一通道内的多个轨道按权重做加权平均：

```text
value  = Σ(trackWeight * trackOpacity * trackValue) / Σ(trackWeight * trackOpacity)
weight = Σ(trackWeight * trackOpacity)
```

- 轨道权重必须为有限非负数。
- 总权重为 0 或没有自然存活内容时，结果 `Active=false`。
- 轨道入场期的 `opacity` 从 0 增加到 1，可用于从透明状态淡入。
- 多个轨道之间的结果不依赖调用顺序。

## 片段排布、重叠与过渡打断

片段按开始时间排序；开始时间相同时按片段 ID 排序，以消除相等时间点的歧义。同一轨道内的片段会按时间顺序生成合成源：

- 没有过渡（`TransitionIn=0`）时，在片段开始处硬切。
- 新片段入场期间，`alpha = easing((t - Start) / TransitionIn)`。
- 入场混合的“旧状态”是新片段开始时刻仍重叠，或恰好在该点结束的旧合成状态。
- 如果旧片段在过渡结束前结束，旧状态会冻结为其结束值，保证过渡期间始终有明确定义的数学结果。
- 已经结束的片段不会跨越正长度的无内容间隔影响后来的片段。
- 过渡进行中再次被新过渡打断时，旧过渡本身成为后续新过渡的确定性旧源。计算只依赖绝对时间，因此不存在“停在中间”的可变运行期状态；任意时刻直接跳转都能重算出相同状态。
- 过渡时长必须满足 `0 <= TransitionIn <= Duration`。

在边界上保持值连续，但缓动曲线的导数可能改变；这是由线性混合和缓动规则定义的正常结果。

## 播放位置、回退、跳转和变速

```go
engine.Seek(t)                  // 直接跳转或回退到绝对时间
engine.SetRate(rate)            // 设置 Tick 使用的正速率
engine.SetPlaying(true)         // 设置播放标记
state, err := engine.Tick(dt)   // position += dt * rate，负值用于本地回退
state, err := engine.TickIdempotent(dt, token)
```

- `Rate` 必须为有限正数。
- `Tick` 支持负 `dt`，位置在 0 处夹紧。
- `Seek` 是显式跳转，允许回退；它发布完整的新播放状态。
- `TickIdempotent` 用非空 `token` 防重复推进：同一个已提交令牌重试不会再次推进。
- 每次成功的播放变更都递增 `CommitSequence`，并通过原子快照发布。

## 运行期变更

`ReplaceTimeline` 替换完整定义；`UpdateTimeline` 给调用方一个隔离深拷贝：

```go
err := engine.UpdateTimeline(func(tl *Timeline) error {
    ch := tl.Channels["x"]
    ch.Tracks[0].Weight = 0.5
    tl.Channels["x"] = ch
    return nil
})
```

变更先在工作副本中完成，再执行完整校验；校验通过后一次性原子发布。失败时旧快照继续服务，不会发布“轨道已改、关键帧未改”的半更新状态。并发采样总是读取旧快照或新快照，不会读到中间副本。

## 多采样方

```go
cursor, _ := motion.NewCursor(engine, 0)
_ = cursor.SetRate(0.5)
_, _ = cursor.Tick(0.1)
sample, _ := cursor.Sample("x")
```

- 游标只保存自己的位置和速率，不修改引擎。
- 慢采样方不持有全局锁等待快采样方，也不会阻止时间线推进。
- 引擎写入使用互斥串行化；采样读取不可变快照指针。

## 存档版本与恢复

当前格式：

```go
const CurrentArchiveVersion = 1
```

存档是完整 JSON：

- `format`：必须为 `motion-sequencer-archive`，旧版本 v0 允许省略或迁移。
- `version`：v0 可加载并迁移；高于当前版本立即拒绝。
- `timeline`：完整时间线。
- `playback`：最近一次已提交的播放状态。

载入流程先解码到临时结构，再迁移、校验全部内容，最后才替换原子快照。因此：

- 新版本无法识别时不修改当前状态。
- JSON 损坏或校验失败时不留下部分生效的时间线。
- 重启后调用 `LoadArchive`/`LoadArchiveFile` 即可从最近保存的已提交位置继续。
- `SaveArchiveFile` 使用同目录临时文件、`fsync` 和 `rename` 原子替换。

防止重复推进的调用方应把 `TickIdempotent` 生成的 `LastTickToken` 一起保存，并在重试时使用同一个业务令牌。存档本身不会自动把内存状态写到磁盘；只有显式调用保存方法才会更新恢复点。

## 校验配置项摘要

| 项目 | 规则 |
| --- | --- |
| 通道 ID | map key 非空，值 ID 为空或与 key 相同 |
| 轨道 ID | 同一通道内唯一且非空 |
| 片段 ID | 同一轨道内唯一且非空 |
| 时间 | 开始非负，片段时长为有限正数 |
| 过渡 | `0 <= TransitionIn <= Duration` |
| 权重 | 有限非负数 |
| 播放速率 | 有限正数 |
| 关键帧 | 值有限；偏移在片段内且严格递增 |
| 缓动 | 必须是内置标识符 |
| 存档版本 | 接受 0/1，拒绝其他低版本和所有更高版本 |

## 测试

```bash
go test ./...
go test -race ./...
```

测试覆盖加权叠加、关键帧缓动、重叠过渡与过渡打断、逐步推进和直接跳转、运行期变更、并发采样/写入、旧版存档迁移、未来版本拒绝、坏存档回滚和文件恢复。
