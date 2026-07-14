package main

import "context"

// LLMProvider 大模型供应商接口。
// 每个供应商（DeepSeek、阿里云等）各自实现此接口。
type LLMProvider interface {
	// ChatStream 发起流式聊天请求，通过 channel 返回增量数据。
	// ctx 用于取消请求，流结束后 channel 被关闭。
	ChatStream(ctx context.Context, cfg ProviderConfig, messages []Message) (<-chan Delta, error)
}

// ProviderConfig 供应商配置
type ProviderConfig struct {
	APIKey  string  `json:"api_key"`
	BaseURL string  `json:"base_url"`
	Model   string  `json:"model"`
	Options Options `json:"options"`
}

// Delta SSE 流中的一个增量块
type Delta struct {
	Content      string `json:"content"`       // 增量文本
	Reasoning    string `json:"reasoning"`      // 思考过程（DeepSeek thinking 模式）
	FinishReason string `json:"finish_reason"`  // "stop" / "length" / ""
	Error        error  `json:"-"`              // 错误（不序列化到 JSON）
}

// Message 聊天消息
type Message struct {
	Role    string `json:"role"`    // "system" / "user" / "assistant"
	Content string `json:"content"`
}
