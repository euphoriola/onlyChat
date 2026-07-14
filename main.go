//go:build !cli

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	webview "github.com/jchv/go-webview2"
)

//go:embed frontend.html
var frontendHTML string

// ============================================================
// 全局状态
// ============================================================

var (
	// 活跃的流式请求控制
	activeCtx    context.Context
	activeCancel context.CancelFunc
	activeMu     sync.Mutex
)

// ============================================================
// 供应商注册表
// ============================================================

var providerRegistry = map[string]LLMProvider{
	"deepseek": &DeepSeekProvider{},
}

// ============================================================
// 入口
// ============================================================

func main() {
	// 初始化配置
	if err := InitConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
	}

	// 初始化会话目录
	InitSessions()

	// 准备前端 HTML
	htmlPath := prepareHTML()

	// 创建 WebView 窗口
	devTools := strings.Contains(strings.ToLower(os.Getenv("ONLYCHAT_DEBUG")), "true")
	w := webview.New(devTools)
	defer w.Destroy()

	w.SetTitle("onlyChat")
	w.SetSize(1100, 700, webview.HintNone)

	// ============================================================
	// JS↔Go 绑定
	// ============================================================

	// goChat — 发起流式聊天请求
	w.Bind("goChat", func(sessionID string, message string) (string, error) {
		// 取消上一个活跃请求
		cancelActive()

		// 加载会话
		sess, err := LoadSession(sessionID)
		if err != nil {
			return "", fmt.Errorf("加载会话失败: %w", err)
		}

		// 追加用户消息
		sess.Messages = append(sess.Messages, Message{Role: "user", Content: message})

		// 获取当前配置
		cfg := GetConfig()
		if cfg == nil {
			return "", fmt.Errorf("配置未加载")
		}

		prov := cfg.GetCurrentProvider()
		if prov == nil {
			return "", fmt.Errorf("未找到供应商: %s", cfg.CurrentProvider)
		}
		if prov.APIKey == "" {
			return "", fmt.Errorf("请先配置 API Key")
		}

		providerCfg := cfg.ToProviderConfig()

		// 获取 provider 实现
		llm, ok := providerRegistry[cfg.CurrentProvider]
		if !ok {
			return "", fmt.Errorf("不支持的供应商: %s", cfg.CurrentProvider)
		}

		// 创建 context
		ctx, cancel := context.WithCancel(context.Background())
		activeMu.Lock()
		activeCtx = ctx
		activeCancel = cancel
		activeMu.Unlock()

		// 拷贝必要变量供 goroutine 使用
		sessID := sessionID
		wRef := w

		go func() {
			defer cancel()

			ch, err := llm.ChatStream(ctx, providerCfg, sess.Messages)
			if err != nil {
				wRef.Dispatch(func() {
					wRef.Eval(fmt.Sprintf(`window.onChatError({sessionID:%q, message:%q})`,
						sessID, err.Error()))
				})
				return
			}

			var fullText string
			for delta := range ch {
				if delta.Error != nil {
					wRef.Dispatch(func() {
						wRef.Eval(fmt.Sprintf(`window.onChatError({sessionID:%q, message:%q})`,
							sessID, delta.Error.Error()))
					})
					return
				}

				if delta.Content != "" {
					fullText += delta.Content
					escaped := escapeJSON(delta.Content)
					wRef.Dispatch(func() {
						wRef.Eval(fmt.Sprintf(`window.onChatChunk({sessionID:%q, delta:%s})`,
							sessID, escaped))
					})
				}

				// 思考内容（DeepSeek thinking 模式）暂不显示，仅记录
				if delta.Reasoning != "" {
					fullText += delta.Reasoning
				}
			}

			// 流结束：保存消息并通知前端
			sess.Messages = append(sess.Messages, Message{Role: "assistant", Content: fullText})
			SaveSession(sess)

			wRef.Dispatch(func() {
				wRef.Eval(fmt.Sprintf(`window.onChatDone({sessionID:%q, fullText:%s})`,
					sessID, escapeJSON(fullText)))
			})
		}()

		return `{"ok":true}`, nil
	})

	// goAbort — 取消当前流式请求
	w.Bind("goAbort", func() (string, error) {
		cancelActive()
		return `{"ok":true}`, nil
	})

	// goLoadSession — 加载会话详情
	w.Bind("goLoadSession", func(sessionID string) (string, error) {
		sess, err := LoadSession(sessionID)
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(sess)
		if err != nil {
			return "", err
		}
		return string(data), nil
	})

	// goCreateSession — 创建新会话
	w.Bind("goCreateSession", func(name string, promptName string) (string, error) {
		meta, err := CreateSession(name, promptName)
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(meta)
		if err != nil {
			return "", err
		}
		return string(data), nil
	})

	// goDeleteSession — 删除会话
	w.Bind("goDeleteSession", func(sessionID string) (string, error) {
		if err := DeleteSession(sessionID); err != nil {
			return "", err
		}
		return `{"ok":true}`, nil
	})

	// goListSessions — 列出所有会话
	w.Bind("goListSessions", func() (string, error) {
		list, err := ListSessions()
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(list)
		if err != nil {
			return "", err
		}
		return string(data), nil
	})

	// goGetConfig — 获取配置
	w.Bind("goGetConfig", func() (string, error) {
		cfg := GetConfig()
		if cfg == nil {
			return "{}", nil
		}
		data, err := json.Marshal(cfg)
		if err != nil {
			return "", err
		}
		return string(data), nil
	})

	// goSaveConfig — 保存配置
	w.Bind("goSaveConfig", func(configJSON string) (string, error) {
		var cfg Config
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return "", fmt.Errorf("解析配置失败: %w", err)
		}
		if err := SaveConfig(configPath, &cfg); err != nil {
			return "", err
		}
		appConfig = &cfg
		return `{"ok":true}`, nil
	})

	// 加载前端 HTML
	fileURL := "file:///" + strings.ReplaceAll(filepath.ToSlash(htmlPath), " ", "%20")
	w.Navigate(fileURL)

	// 进入事件循环（阻塞直到窗口关闭）
	w.Run()
}

// ============================================================
// 辅助函数
// ============================================================

func cancelActive() {
	activeMu.Lock()
	defer activeMu.Unlock()
	if activeCancel != nil {
		activeCancel()
		activeCancel = nil
		activeCtx = nil
	}
}

// escapeJSON 将字符串转为 JSON 安全字符串（用于嵌入 JS）。
func escapeJSON(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

// prepareHTML 将嵌入的 HTML 写入 exe 同级目录，返回文件路径。
func prepareHTML() string {
	exePath, _ := os.Executable()
	htmlPath := filepath.Join(filepath.Dir(exePath), "frontend.html")
	os.WriteFile(htmlPath, []byte(frontendHTML), 0644)
	return htmlPath
}
