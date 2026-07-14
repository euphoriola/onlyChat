package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DeepSeekProvider DeepSeek API 实现（兼容 OpenAI Chat Completions 格式）
type DeepSeekProvider struct{}

// chatReq OpenAI 兼容的请求体
type chatReq struct {
	Model            string    `json:"model"`
	Messages         []Message `json:"messages"`
	Stream           bool      `json:"stream"`
	Temperature      *float64  `json:"temperature,omitempty"`
	MaxTokens        *int      `json:"max_tokens,omitempty"`
	ReasoningEffort  *string   `json:"reasoning_effort,omitempty"`
	Thinking         *thinkingBody `json:"thinking,omitempty"`
}

type thinkingBody struct {
	Type string `json:"type"` // "enabled"
}

// chatRespChunk SSE 响应中的一个 data chunk
type chatRespChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// ChatStream 实现 LLMProvider 接口。
func (p *DeepSeekProvider) ChatStream(ctx context.Context, cfg ProviderConfig, messages []Message) (<-chan Delta, error) {
	reqBody := chatReq{
		Model:    cfg.Model,
		Messages: messages,
		Stream:   true,
	}

	if cfg.Options.Temperature != nil {
		reqBody.Temperature = cfg.Options.Temperature
	}
	if cfg.Options.MaxTokens != nil {
		reqBody.MaxTokens = cfg.Options.MaxTokens
	}
	if cfg.Options.ReasoningEffort != nil && *cfg.Options.ReasoningEffort != "" {
		reqBody.ReasoningEffort = cfg.Options.ReasoningEffort
	}
	if cfg.Options.Thinking != nil && *cfg.Options.Thinking {
		reqBody.Thinking = &thinkingBody{Type: "enabled"}
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	url := strings.TrimSuffix(cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("API 返回错误 (%d): %s", resp.StatusCode, string(body))
	}

	ch := make(chan Delta, 64)

	go func() {
		defer resp.Body.Close()
		defer close(ch)

		scanner := bufio.NewScanner(resp.Body)
		// SSE 行可能很长（含中文），增大 buffer
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			// 检查 ctx 是否已取消
			select {
			case <-ctx.Done():
				return
			default:
			}

			line := scanner.Text()

			// 跳过空行和注释
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			// 检查 [DONE]
			if line == "data: [DONE]" {
				return
			}

			// 解析 data: {...}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			jsonStr := strings.TrimPrefix(line, "data: ")

			var chunk chatRespChunk
			if err := json.Unmarshal([]byte(jsonStr), &chunk); err != nil {
				// 忽略解析错误（某些行可能不是有效 JSON）
				continue
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			choice := chunk.Choices[0]
			delta := Delta{}

			if choice.Delta.Content != "" {
				delta.Content = choice.Delta.Content
			}
			if choice.Delta.ReasoningContent != "" {
				delta.Reasoning = choice.Delta.ReasoningContent
			}
			if choice.FinishReason != nil {
				delta.FinishReason = *choice.FinishReason
			}

			// 只有有内容或结束标记时才发送
			if delta.Content != "" || delta.Reasoning != "" || delta.FinishReason != "" {
				ch <- delta
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- Delta{Error: fmt.Errorf("读取流失败: %w", err)}
		}
	}()

	return ch, nil
}
