package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SessionIndex 会话索引文件 (sessions/index.json)
type SessionIndex struct {
	Sessions []SessionMeta `json:"sessions"`
}

// SessionMeta 会话元数据（索引中的简要信息）
type SessionMeta struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	PromptName   string `json:"prompt_name"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	MessageCount int    `json:"message_count"`
}

// Session 单个会话的完整数据 (sessions/{id}.json)
type Session struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	PromptName string    `json:"prompt_name"`
	CreatedAt  string    `json:"created_at"`
	UpdatedAt  string    `json:"updated_at"`
	Messages   []Message `json:"messages"`
}

// ============================================================
// 路径工具
// ============================================================

var sessionsDir string

// InitSessions 初始化会话存储目录。
func InitSessions() {
	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)
	sessionsDir = filepath.Join(exeDir, "sessions")
	os.MkdirAll(sessionsDir, 0755)
}

func indexPath() string {
	return filepath.Join(sessionsDir, "index.json")
}

func sessionPath(id string) string {
	return filepath.Join(sessionsDir, id+".json")
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// ============================================================
// 索引操作
// ============================================================

func loadIndex() (*SessionIndex, error) {
	data, err := os.ReadFile(indexPath())
	if os.IsNotExist(err) {
		return &SessionIndex{}, nil
	}
	if err != nil {
		return nil, err
	}
	var idx SessionIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, err
	}
	if idx.Sessions == nil {
		idx.Sessions = []SessionMeta{}
	}
	return &idx, nil
}

func saveIndex(idx *SessionIndex) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(indexPath(), data, 0644)
}

// ============================================================
// 会话 CRUD
// ============================================================

// CreateSession 创建新会话。
func CreateSession(name, promptName string) (*SessionMeta, error) {
	id := newID()
	now := nowISO()

	// 构建 system message（如果指定了 prompt 模板）
	var messages []Message
	if promptName != "" {
		cfg := GetConfig()
		if cfg != nil {
			if p := cfg.FindPrompt(promptName); p != nil {
				messages = []Message{
					{Role: "system", Content: p.Content},
				}
			}
		}
	}
	if messages == nil {
		messages = []Message{}
	}

	sess := &Session{
		ID:         id,
		Name:       name,
		PromptName: promptName,
		CreatedAt:  now,
		UpdatedAt:  now,
		Messages:   messages,
	}

	// 写入会话文件
	sessData, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(sessionPath(id), sessData, 0644); err != nil {
		return nil, err
	}

	// 更新索引
	idx, err := loadIndex()
	if err != nil {
		return nil, err
	}
	meta := SessionMeta{
		ID:           id,
		Name:         name,
		PromptName:   promptName,
		CreatedAt:    now,
		UpdatedAt:    now,
		MessageCount: len(messages),
	}
	idx.Sessions = append(idx.Sessions, meta)
	if err := saveIndex(idx); err != nil {
		return nil, err
	}

	return &meta, nil
}

// LoadSession 加载会话详情。
func LoadSession(id string) (*Session, error) {
	data, err := os.ReadFile(sessionPath(id))
	if err != nil {
		return nil, fmt.Errorf("读取会话失败: %w", err)
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("解析会话失败: %w", err)
	}
	return &sess, nil
}

// SaveSession 保存会话（追加消息后调用）。
func SaveSession(sess *Session) error {
	sess.UpdatedAt = nowISO()
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(sessionPath(sess.ID), data, 0644); err != nil {
		return err
	}

	// 更新索引
	idx, err := loadIndex()
	if err != nil {
		return err
	}
	for i := range idx.Sessions {
		if idx.Sessions[i].ID == sess.ID {
			idx.Sessions[i].UpdatedAt = sess.UpdatedAt
			idx.Sessions[i].MessageCount = len(sess.Messages)
			break
		}
	}
	return saveIndex(idx)
}

// DeleteSession 删除会话（JSON 文件 + 索引条目）。
func DeleteSession(id string) error {
	// 删除会话文件（忽略不存在的错误）
	os.Remove(sessionPath(id))

	// 更新索引
	idx, err := loadIndex()
	if err != nil {
		return err
	}
	filtered := make([]SessionMeta, 0, len(idx.Sessions))
	for _, s := range idx.Sessions {
		if s.ID != id {
			filtered = append(filtered, s)
		}
	}
	idx.Sessions = filtered
	return saveIndex(idx)
}

// ListSessions 返回所有会话的元数据列表。
func ListSessions() ([]SessionMeta, error) {
	idx, err := loadIndex()
	if err != nil {
		return nil, err
	}
	return idx.Sessions, nil
}
