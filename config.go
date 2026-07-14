package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config 对应 config.json 的完整结构
type Config struct {
	CurrentProvider string     `json:"current_provider"`
	CurrentModel    string     `json:"current_model"`
	Providers       []Provider `json:"providers"`
	Prompts         []Prompt   `json:"prompts"`
	Options         Options    `json:"options"`
}

// Provider 单个供应商的配置
type Provider struct {
	Name    string   `json:"name"`
	APIKey  string   `json:"api_key"`
	BaseURL string   `json:"base_url"`
	Models  []string `json:"models"`
}

// Prompt 模板
type Prompt struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// Options 高级参数
type Options struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxTokens       *int     `json:"max_tokens,omitempty"`
	ReasoningEffort *string  `json:"reasoning_effort,omitempty"` // "high"/"medium"/"low"
	Thinking        *bool    `json:"thinking,omitempty"`
}

// LoadConfig 从路径读取并解析 config.json。
// 如果文件不存在，生成默认配置并写入。
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := DefaultConfig()
		if saveErr := SaveConfig(path, cfg); saveErr != nil {
			return nil, fmt.Errorf("生成默认配置失败: %w", saveErr)
		}
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 填充默认值
	if cfg.Options.Temperature == nil {
		t := 0.7
		cfg.Options.Temperature = &t
	}
	if cfg.Options.MaxTokens == nil {
		mt := 4096
		cfg.Options.MaxTokens = &mt
	}

	return &cfg, nil
}

// SaveConfig 将配置写入 JSON 文件。
func SaveConfig(path string, cfg *Config) error {
	// 确保目录存在
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}
	return nil
}

// DefaultConfig 返回默认配置。
func DefaultConfig() *Config {
	t := 0.7
	mt := 4096
	re := "high"
	thinking := false

	return &Config{
		CurrentProvider: "deepseek",
		CurrentModel:    "deepseek-chat",
		Providers: []Provider{
			{
				Name:    "deepseek",
				APIKey:  "",
				BaseURL: "https://api.deepseek.com",
				Models:  []string{"deepseek-chat", "deepseek-reasoner"},
			},
		},
		Prompts: []Prompt{
			{Name: "默认", Content: "你是一个有帮助的AI助手。"},
			{Name: "代码助手", Content: "你是一个专业的编程助手。回答时请给出可直接运行的代码，并附上简洁的解释。"},
		},
		Options: Options{
			Temperature:     &t,
			MaxTokens:       &mt,
			ReasoningEffort: &re,
			Thinking:        &thinking,
		},
	}
}

// GetCurrentProvider 返回当前选中的供应商配置。
// 如果找不到，返回 nil。
func (c *Config) GetCurrentProvider() *Provider {
	for i := range c.Providers {
		if c.Providers[i].Name == c.CurrentProvider {
			return &c.Providers[i]
		}
	}
	return nil
}

// ToProviderConfig 将 Config 转为 LLMProvider 可用的 ProviderConfig。
func (c *Config) ToProviderConfig() ProviderConfig {
	p := c.GetCurrentProvider()
	if p == nil {
		return ProviderConfig{}
	}
	return ProviderConfig{
		APIKey:  p.APIKey,
		BaseURL: p.BaseURL,
		Model:   c.CurrentModel,
		Options: c.Options,
	}
}

// FindPrompt 按名称查找 prompt 模板。
func (c *Config) FindPrompt(name string) *Prompt {
	for i := range c.Prompts {
		if c.Prompts[i].Name == name {
			return &c.Prompts[i]
		}
	}
	return nil
}

// ============================================================
// 运行时配置（启动后缓存于内存中）
// ============================================================

var appConfig *Config
var configPath string

// InitConfig 初始化全局配置。
func InitConfig() error {
	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)
	configPath = filepath.Join(exeDir, "config.json")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	appConfig = cfg
	return nil
}

// GetConfig 返回当前内存中的配置。
func GetConfig() *Config {
	return appConfig
}

// ReloadConfig 重新加载配置（保存后调用）。
func ReloadConfig() error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	appConfig = cfg
	return nil
}
