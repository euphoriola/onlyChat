# onlyChat 设计方案 v3

> 基于用户反馈确认。将大模型对话包装为桌面 GUI 应用，Go + WebView2，编译为单个 .exe，双击即用。

---

## 1. 项目目标

做一个最简单的桌面 AI 聊天应用：

- **双击 exe → 弹出窗口 → 选择/新建会话 → 开始聊天**
- 不需要浏览器、不需要命令行、不需要安装任何运行时
- 左侧会话列表，右侧聊天区，支持多会话
- 流式输出，AI 回复逐字显示
- 支持 Markdown 渲染（代码高亮）

---

## 2. 参考项目

`sy_grouping_go` — 同样技术栈：Go + `github.com/jchv/go-webview2`，HTML 通过 `//go:embed` 嵌入 exe，JS ↔ Go 通过 `w.Bind()` / `w.Eval()` 通信。

本项目关键区别：**需要流式推送**（SSE chunk by chunk → `w.Eval()` 推给前端逐字渲染），而不是一次性返回计算结果。

---

## 3. 技术选型

| 层面 | 选型 | 理由 |
|------|------|------|
| 桌面窗口 | `github.com/jchv/go-webview2` | Windows 原生 WebView2，纯 Go 无 CGO |
| 前端 | 单文件 `frontend.html` | `//go:embed` 嵌入 exe，内联 CSS/JS + marked.js |
| Markdown 渲染 | `marked.js` + `highlight.js` | 两个库 JS 源码直接内联到 HTML（~50KB） |
| HTTP 请求 | Go `net/http` | Go 侧发 HTTP 到大模型 API（SSE） |
| 流式输出 | goroutine + `w.Dispatch()` + `w.Eval()` | 异步读取 SSE，逐 chunk 推给前端 |
| 配置 | `config.json` | 程序启动时读取 |
| 会话存储 | JSON 文件（每个会话一个文件） | 简单、可读、便于备份迁移 |
| 第三方依赖 | 仅 1 个：`go-webview2` | 其余全部 Go 标准库 |

---

## 4. 前端界面设计

### 4.1 整体布局

```
┌──────────────────────────────────────────────────────────────────┐
│  🤖 onlyChat                                          [_][□][×]  │  标题栏
├────────────┬─────────────────────────────────────────────────────┤
│            │  会话标题 / Prompt 名称                              │
│  会话列表   │  ┌──────────────────────────────────────────────┐   │
│            │  │  用户消息气泡                                  │   │
│  ┌──────┐  │  └──────────────────────────────────────────────┘   │
│  │会话 1 │  │       ┌──────────────────────────────────────┐     │  聊天区域
│  │会话 2 │  │       │ AI 回复消息气泡（流式逐字显示）       │     │
│  │会话 3 │  │       │ 支持 Markdown 渲染 + 代码高亮        │     │
│  ├──────┤  │       └──────────────────────────────────────┘     │
│  │+ 新建 │  │                                                    │
│  └──────┘  │  ...更多历史消息...                                  │
│            │                                                    │
│            ├─────────────────────────────────────────────────────┤
│            │  [___________________________________________] [发送]│  输入区域
│            │  Enter 发送，Shift+Enter 换行                        │
└────────────┴─────────────────────────────────────────────────────┘
```

- **左侧边栏**：会话列表（可切换、可删除、可新建），固定宽度 ~220px
- **右侧上**：消息列表，flex-grow，overflow-y 滚动，用户和 AI 气泡交替
- **右侧下**：多行文本输入框 + 发送按钮，固定底部
- **Enter** 发送消息，**Shift+Enter** 换行

### 4.2 设置弹窗

点击标题栏 ⚙ 图标弹出：

```
┌────────────────────────────────────────────┐
│  配置                                 [×]  │
│                                            │
│  供应商:     [▼ deepseek]                  │  ← 切换供应商时自动切换下方模型列表
│  模型:       [▼ deepseek-chat]             │  ← 当前供应商的 models 数组
│  ─────────────────────────────             │
│  API Key:    [sk-xxxxxxxxxx          ]     │
│  Base URL:   [https://api.deepseek.com]    │
│                                            │
│  ── Prompt 模板 ──                         │
│  选择模板:   [▼ 默认 / 代码助手]            │
│                                            │
│  ── 高级参数（折叠，默认隐藏）──             │
│  Temperature:  [0.7          ]             │
│  Max Tokens:   [4096         ]             │
│  推理力度:     [▼ high/medium/low]          │  ← DeepSeek reasoning_effort
│  思考模式:     [ ] 启用 (thinking)          │  ← DeepSeek thinking
│                                            │
│          [取消]  [保存]                     │
└────────────────────────────────────────────┘
```

- 所有值从 `config.json` 读取
- 点击保存后写回 `config.json`
- 切换供应商时，API Key / Base URL / 模型列表联动更新

### 4.3 新建会话流程

```
点击 "+ 新建"
    │
    ▼
┌──────────────────────────┐
│  新建会话                 │
│                          │
│  会话名称: [_________]    │
│  Prompt 模板: [▼ 选择]   │
│                          │
│     [取消]  [创建]       │
└──────────────────────────┘
    │
    ▼
会话创建成功 → 出现在左侧列表 → 自动选中 → 右侧显示 system prompt（可选），等待用户输入
```

---

## 5. 文件结构

```
onlyChat/
├── main.go              # 入口：WebView 窗口 + Go↔JS 绑定注册
├── provider.go           # LLMProvider 接口定义 + 公共类型
├── deepseek.go          # DeepSeek API 调用（请求构造 + SSE 解析）
├── config.go            # config.json 读取、解析、写入
├── session.go           # 会话 CRUD（JSON 文件读写）
├── frontend.html         # 聊天 UI（内联 CSS + marked.js + highlight.js）
├── config.json           # 配置文件（用户填写）
├── go.mod
├── go.sum
├── design.md            # 本设计文档
├── what_i_want.md       # 用户需求原始描述
└── README.md
```

6 个 Go 源文件 + 1 个 HTML 前端文件。每个文件职责单一、代码量小。

---

## 6. config.json 设计（根据 Q2 反馈重新设计）

### 6.1 核心思路

用户指出：provider / api_key / base_url 是**绑定关系**，应该一起存储；model 是 provider 的下级数组。因此设计为：

```
providers: [
  { name, api_key, base_url, models[] }
]
current_provider: "deepseek"
current_model: "deepseek-chat"
prompts: [...]
```

### 6.2 完整结构

```jsonc
{
  // 当前使用的供应商和模型
  "current_provider": "deepseek",
  "current_model": "deepseek-chat",

  // 供应商列表（每个供应商一组独立的 key + url + models）
  "providers": [
    {
      "name": "deepseek",
      "api_key": "sk-xxxxxxxxxxxxxxxxxxxxxxxx",
      "base_url": "https://api.deepseek.com",
      "models": [
        "deepseek-chat",
        "deepseek-reasoner",
        "deepseek-v4-pro"
      ]
    },
    {
      "name": "aliyun",
      "api_key": "sk-xxxxxxxxxxxxxxxxxxxxxxxx",
      "base_url": "https://dashscope.aliyuncs.com/compatible-mode/v1",
      "models": [
        "qwen-turbo",
        "qwen-plus",
        "qwen-max"
      ]
    }
  ],

  // Prompt 模板
  "prompts": [
    {
      "name": "默认",
      "content": "你是一个有帮助的AI助手。"
    },
    {
      "name": "代码助手",
      "content": "你是一个专业的编程助手。回答时请给出可直接运行的代码，并附上简洁的解释。"
    }
  ],

  // 高级参数（可选，有默认值）
  "options": {
    "temperature": 0.7,
    "max_tokens": 4096,
    "reasoning_effort": "high",
    "thinking": false
  }
}
```

### 6.3 字段说明

| 字段 | 必填 | 说明 |
|------|------|------|
| `current_provider` | 是 | 默认使用的供应商名称，必须存在于 `providers[].name` |
| `current_model` | 是 | 默认使用的模型，必须存在于对应 provider 的 `models[]` |
| `providers` | 是 | 供应商数组，至少一个 |
| `providers[].name` | 是 | 供应商标识，如 `"deepseek"` |
| `providers[].api_key` | 是 | 该供应商的 API 密钥 |
| `providers[].base_url` | 是 | 该供应商的 API 地址（不含 `/chat/completions`） |
| `providers[].models` | 是 | 该供应商可用的模型列表 |
| `prompts` | 是 | Prompt 模板数组，至少一个 |
| `prompts[].name` | 是 | 模板名称，显示在下拉选择框 |
| `prompts[].content` | 是 | 模板内容，作为 system message 发送 |
| `options` | 否 | 高级参数，不填则用默认值 |
| `options.temperature` | 否 | 采样温度，默认 0.7 |
| `options.max_tokens` | 否 | 最大输出 token 数，默认 4096 |
| `options.reasoning_effort` | 否 | DeepSeek 推理力度：`"high"` / `"medium"` / `"low"`，仅 DeepSeek 有效 |
| `options.thinking` | 否 | DeepSeek 思考模式，开启后会在回答前输出推理过程，默认 false |

### 6.4 options 的扩展性

不同供应商可能有不同参数。`options` 是一个通用 map，每个 provider 忽略自己不认识的字段：

```go
// config.go
type Options struct {
    Temperature     *float64 `json:"temperature,omitempty"`
    MaxTokens       *int     `json:"max_tokens,omitempty"`
    ReasoningEffort *string  `json:"reasoning_effort,omitempty"` // "high"/"medium"/"low"
    Thinking        *bool    `json:"thinking,omitempty"`         // thinking mode
    // 未来扩展字段直接加在这里
}
```

---

## 7. 架构与数据流

### 7.1 整体流程

```
onlyChat.exe 启动
    │
    ├─ 读取 config.json → 解析配置 → 存到 Go 内存
    ├─ 读取 sessions/ 目录 → 加载会话列表
    ├─ 创建 WebView 窗口 → 注入初始数据 → 渲染前端
    │
    └─ 进入事件循环 (w.Run())

用户在 WebView 窗口中操作：
    │
    ├─ 发送消息 → JS 调 goChat(sessionID, message)
    │              │
    │              ├─ Go 从内存获取当前 provider 配置
    │              ├─ Go 拼装 Messages（system prompt + 历史 + 新消息）
    │              ├─ Go 调用 provider.ChatStream() → SSE 流
    │              ├─ goroutine 读取 channel
    │              │   ├─ delta content → w.Dispatch() → w.Eval("onChunk(...)")
    │              │   ├─ 流结束 → w.Eval("onDone(...)") → 保存到 JSON 文件
    │              │   └─ 出错 → w.Eval("onError(...)")
    │              └─ (返回)
    │
    ├─ 新建会话 → JS 调 goCreateSession(name, promptName)
    │              └─ Go 在 sessions/ 目录创建 {uuid}.json
    │
    ├─ 切换会话 → JS 调 goLoadSession(sessionID)
    │              └─ Go 读取 sessions/{id}.json → 返回消息列表
    │
    ├─ 删除会话 → JS 调 goDeleteSession(sessionID)
    │
    ├─ 读取配置 → JS 调 goGetConfig()
    │
    └─ 保存配置 → JS 调 goSaveConfig(configJSON)
                   └─ Go 写入 config.json + 更新内存
```

### 7.2 流式输出通信模式

与 `sy_grouping_go` 中的同步调用不同，聊天需要异步流式推送：

```
时间线 →

JS 侧:
  goChat(sessionID, msg) ──→ 立即返回 {ok:true}
                                    │
  onChunk({delta:"你"}) ←──────────┘ (chunk 1)
  onChunk({delta:"好"}) ←──────────┘ (chunk 2)
  onChunk({delta:"！"}) ←──────────┘ (chunk 3)
  onDone({fullText:"你好！..."}) ←─┘ (流结束)
  onError({message:"..."})  ←──────┘ (如果出错，可选)

Go 侧:
  Bind("goChat", func(sessionID, msg string) {
    go func() {
      // 1. 加载会话历史
      // 2. 拼装 messages
      // 3. provider.ChatStream(ctx, cfg, messages) → chan Delta
      for delta := range ch {
        if delta.Error != nil {
          w.Dispatch(func() { w.Eval(`onError(...)`) })
          return
        }
        fullText += delta.Content
        w.Dispatch(func() {
          w.Eval(`onChatChunk({sessionID:"...", delta:"..."})`)
        })
      }
      // 4. 保存会话到 JSON
      w.Dispatch(func() { w.Eval(`onDone(...)`) })
    }()
    return `{"ok":true}`, nil
  })
```

---

## 8. Go↔JS 接口设计

| 方向 | 函数名 | 参数 | 返回 |
|------|--------|------|------|
| JS → Go | `goChat(sessionID, message)` | 会话ID、用户消息文本 | `{ok: true}` |
| JS → Go | `goAbort(sessionID)` | 会话ID | `{ok: true}` |
| JS → Go | `goLoadSession(sessionID)` | 会话ID | `{messages: [...]}` (JSON 字符串) |
| JS → Go | `goCreateSession(name, promptName)` | 名称、prompt模板名 | `{session: {...}}` (JSON 字符串) |
| JS → Go | `goDeleteSession(sessionID)` | 会话ID | `{ok: true}` |
| JS → Go | `goListSessions()` | 无 | `{sessions: [...]}` (JSON 字符串) |
| JS → Go | `goGetConfig()` | 无 | 完整 config.json 内容 (JSON 字符串) |
| JS → Go | `goSaveConfig(configJSON)` | 新配置 JSON 字符串 | `{ok: true}` |
| Go → JS | `window.onChatChunk(deltaJSON)` | `{sessionID, delta}` | — |
| Go → JS | `window.onChatDone(resultJSON)` | `{sessionID, fullText}` | — |
| Go → JS | `window.onChatError(errorJSON)` | `{sessionID, message}` | — |

---

## 9. 多供应商架构

### 9.1 LLMProvider 接口

```go
// provider.go

type LLMProvider interface {
    // ChatStream 发送消息并以 channel 返回 SSE delta 流。
    // ctx 用于取消，流结束后 channel 被关闭。
    ChatStream(ctx context.Context, cfg ProviderConfig, messages []Message) (<-chan Delta, error)
}

type ProviderConfig struct {
    APIKey  string
    BaseURL string
    Model   string
    Options Options  // 高级参数（temperature, max_tokens, thinking 等）
}

type Delta struct {
    Content      string // 增量文本（delta）
    Reasoning    string // 思考过程（DeepSeek thinking 模式下的 reasoning_content）
    FinishReason string // "stop" / "length" / "" (空表示未结束)
    Error        error
}

type Message struct {
    Role    string `json:"role"`    // "system" / "user" / "assistant"
    Content string `json:"content"`
}
```

### 9.2 DeepSeek 实现（v1）

```go
// deepseek.go

type DeepSeekProvider struct{}

func (p *DeepSeekProvider) ChatStream(ctx context.Context, cfg ProviderConfig, messages []Message) (<-chan Delta, error) {
    // 1. 构造请求体
    //    - model: cfg.Model
    //    - messages: [...]
    //    - stream: true
    //    - temperature: cfg.Options.Temperature (if set)
    //    - max_tokens: cfg.Options.MaxTokens (if set)
    //    - reasoning_effort: cfg.Options.ReasoningEffort (if set)  ← 官方示例
    //    - extra_body: {thinking: {type: "enabled"}} (if thinking=true) ← 官方示例
    //
    // 2. POST {cfg.BaseURL}/chat/completions
    //    Authorization: Bearer {cfg.APIKey}
    //
    // 3. bufio.Scanner 逐行读取 SSE
    //    解析 JSON → delta.choices[0].delta.content
    //    同时检查 delta.choices[0].delta.reasoning_content（思考模式）
    //
    // 4. 通过 channel 发送 Delta
}

// 根据 DeepSeek 官方示例，API 兼容 OpenAI 格式，但多了：
//   - reasoning_effort: "high" | "medium" | "low"
//   - extra_body.thinking.type: "enabled"
```

### 9.3 供应商注册

```go
// main.go

var providerRegistry = map[string]LLMProvider{
    "deepseek": &DeepSeekProvider{},
    // v2: "aliyun": &AliyunProvider{},
}
```

### 9.4 扩展方式（v2+）

新增供应商仅需：
1. 创建 `aliyun.go`，实现 `LLMProvider` 接口
2. 在 `providerRegistry` 中注册
3. 在 `config.json` 的 `providers` 数组中添加配置

---

## 10. 会话与历史记录存储

### 10.1 存储方案

**每个会话一个 JSON 文件**，存储在 exe 同级 `sessions/` 目录。

### 10.2 目录结构

```
onlyChat.exe 同级目录
├── sessions/
│   ├── index.json          # 会话索引
│   ├── {uuid_1}.json       # 会话 1 的完整数据
│   ├── {uuid_2}.json       # 会话 2 的完整数据
│   └── ...
└── config.json
```

### 10.3 文件格式

**index.json**:
```json
{
  "sessions": [
    {
      "id": "a1b2c3d4",
      "name": "代码问题讨论",
      "prompt_name": "代码助手",
      "created_at": "2026-07-14T10:30:00Z",
      "updated_at": "2026-07-14T15:20:00Z",
      "message_count": 12
    }
  ]
}
```

**{uuid}.json**:
```json
{
  "id": "a1b2c3d4",
  "name": "代码问题讨论",
  "prompt_name": "代码助手",
  "created_at": "2026-07-14T10:30:00Z",
  "updated_at": "2026-07-14T15:20:00Z",
  "messages": [
    {"role": "system", "content": "你是一个专业的编程助手..."},
    {"role": "user", "content": "Go 语言怎么读取 JSON 文件？"},
    {"role": "assistant", "content": "使用 encoding/json 包..."},
    {"role": "user", "content": "能给我一个完整示例吗？"}
  ]
}
```

### 10.4 读写时机

| 操作 | 时机 |
|------|------|
| 加载会话列表 | 程序启动时（读取 `index.json`） |
| 加载会话详情 | 用户点击切换会话时（读取 `{uuid}.json`） |
| 保存消息 | **每轮 AI 回复完毕后**（流结束后），写入 `{uuid}.json` + 更新 `index.json` |
| 新建会话 | 在 `index.json` 追加 + 创建 `{uuid}.json` |
| 删除会话 | 从 `index.json` 移除 + 删除 `{uuid}.json` |

---

## 11. Prompt 模板系统

### 11.1 工作流

```
用户新建会话
    │
    ├─ 从 config.json 读取 prompts 列表
    ├─ 前端展示下拉选择框
    │
    ├─ 用户选择 "代码助手"
    ├─ 该 prompt 作为 system message 存入会话 messages[0]
    │
    └─ 发送给 API:
       [
         {role: "system", content: "你是一个专业的编程助手..."},
         {role: "user",   content: "Go 语言怎么读取 JSON 文件？"}
       ]
```

### 11.2 Prompt 的管理

- **v1**：用户手动编辑 `config.json` 中的 `prompts` 数组
- **v2**：在设置弹窗中增删改 prompt 模板

---

## 12. 错误处理

| 错误场景 | 处理方式 |
|----------|----------|
| `config.json` 不存在 | 首次启动生成默认模板，弹出设置引导 |
| API Key 为空 | 前端提示"请先配置 API Key"，引导打开设置 |
| 网络错误（连接失败、超时） | 显示"连接失败：请检查网络和 Base URL" |
| API 返回 401 | 显示"API Key 无效" |
| API 返回 429 | 显示"请求过于频繁，请稍后再试" |
| API 返回 500 | 显示"服务器错误：xxx" |
| SSE 流中断 | 保留已收到的内容 + 显示"连接中断"提示 |
| 会话文件读写失败 | 显示具体错误，不影响 UI 操作 |

---

## 13. 开发顺序

### Phase 1: 骨架搭建
1. `go mod init onlyChat` + `go get github.com/jchv/go-webview2`
2. `config.go` — 读取/解析 `config.json`，生成默认配置
3. `main.go` — 创建 WebView 窗口，加载 `frontend.html`，注册 JS 绑定占位
4. `frontend.html` — 静态布局（左侧边栏 + 右侧聊天区 + 设置弹窗 HTML）

### Phase 2: 核心聊天（非流式，验证连通性）
5. `provider.go` — `LLMProvider` 接口 + 公共类型定义
6. `deepseek.go` — DeepSeek API 调用（先非流式 `stream: false`，验证能通）
7. `main.go` — 注册 `goChat()` 绑定
8. `frontend.html` — 消息发送/显示逻辑（普通回复）
9. 验证：发送一条消息，收到完整回复

### Phase 3: 流式输出
10. `deepseek.go` — 改为 SSE 流式解析 + channel 返回 delta
11. `main.go` — goroutine + `w.Dispatch()` + `w.Eval()` 异步推送
12. `frontend.html` — `onChunk/onDone/onError` 回调实现逐字显示 + Markdown 渲染

### Phase 4: 会话管理
13. `session.go` — 会话 CRUD（index.json + {uuid}.json 读写）
14. `main.go` — 注册会话相关 JS 绑定（goCreateSession / goDeleteSession / goLoadSession / goListSessions）
15. `frontend.html` — 会话列表交互（新建/切换/删除/高亮当前）

### Phase 5: 配置管理 + Prompt
16. 完善 `config.go` — 支持 `goSaveConfig()` 写回
17. `main.go` — 注册 `goGetConfig()` / `goSaveConfig()` 绑定
18. `frontend.html` — 设置弹窗逻辑 + 新建会话时选择 prompt 模板

### Phase 6: Markdown 渲染
19. `frontend.html` — 内联 `marked.js` + `highlight.js` 源码（或 CDN 内联）
20. AI 气泡渲染：纯文本 → 调用 `marked.parse()` → 插入 HTML

### Phase 7: 发布
21. 调试编译：`go build -o onlyChat.exe .`（保留控制台）
22. 正式发布：`go build -ldflags="-s -w -H windowsgui" -o onlyChat.exe .`

---

## 14. 构建命令

```powershell
# 开发调试（保留控制台，可看日志）
go build -o onlyChat.exe .

# 正式发布（无黑框，体积最小）
go build -ldflags="-s -w -H windowsgui" -o onlyChat.exe .

# 仅检查编译
go build .

# 开发时快速运行
go run .
```

预期 exe 体积：约 8-15 MB（Go runtime + WebView2 相关）。

---

## 15. 已确认的所有决定

| # | 决定 | 说明 |
|----|------|------|
| 1 | 会话存储 | JSON 文件，每个会话一个文件，存 sessions/ 目录 |
| 2 | config.json 结构 | providers 数组 + 当前 provider/model + prompts + options |
| 3 | provider/api_key/base_url | 绑定为 providers 数组的子对象，切换供应商时联动 |
| 4 | models | 每个 provider 的 models 数组 |
| 5 | Markdown 渲染 | 必须支持，前端内联 marked.js + highlight.js |
| 6 | 多会话 | v1 必须，左侧会话列表 |
| 7 | 消息存盘时机 | AI 回复全部完成后一次性存入 JSON 文件 |
| 8 | 错误处理 | v1 处理 config 缺失、网络错误、API 错误码、SSE 中断 |
| 9 | 窗口 | 标题 `onlyChat`，无图标，带 3 个窗口按钮 |
| 10 | DeepSeek | v1 唯一供应商，兼容 OpenAI 格式，预留 reasoning_effort + thinking |
| 11 | Prompt 模板 | 存 config.json，v1 手动编辑，v2 UI 管理 |

---

## 16. v1 明确不做的事情（less is more）

- ❌ 没有多供应商（只有 DeepSeek）
- ❌ 没有对话分支/编辑/重新生成
- ❌ 没有 Prompt 模板的 UI 管理（手动编辑 config.json）
- ❌ 没有快捷键自定义
- ❌ 没有导出/导入
- ❌ 没有搜索对话
- ❌ 不支持图片/多模态
- ❌ 不支持 Function Calling / Tool Use
- ❌ 没有暗黑模式（后续加）
- ❌ 没有窗口图标
