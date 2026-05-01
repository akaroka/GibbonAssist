// Package llm provides text polishing via OpenAI-compatible chat API.
package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config for OpenAI-compatible API.
type Config struct {
	URL    string `json:"url"`    // e.g. "https://api.openai.com/v1"
	Key    string `json:"key"`    // API key
	Model  string `json:"model"`  // e.g. "gpt-4o-mini"
	Prompt string `json:"prompt"` // system prompt for polishing

	Timeout time.Duration // request timeout (not from JSON, set in code)
}

// DefaultPrompt for Chinese ASR text polishing.
const DefaultPrompt = "你是中文语音转文字后处理助手，请修正错别字、补全语义、处理语气词，并给出更通顺准确的表达。"

// Polisher calls an OpenAI-compatible chat API to polish text.
type Polisher struct {
	cfg    Config
	client *http.Client
}

// NewPolisher creates a polisher. If cfg.Prompt is empty, DefaultPrompt is used.
func NewPolisher(cfg Config) *Polisher {
	if cfg.Prompt == "" {
		cfg.Prompt = DefaultPrompt
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Polisher{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
	}
}

// IsConfigured returns true when both URL and Key are set.
func (p *Polisher) IsConfigured() bool {
	return strings.TrimSpace(p.cfg.URL) != "" && strings.TrimSpace(p.cfg.Key) != ""
}

// Polish sends text to the LLM for polishing and returns the result.
func (p *Polisher) Polish(text string) (string, error) {
	if !p.IsConfigured() {
		return "", fmt.Errorf("llm not configured: missing url or key")
	}

	body := map[string]interface{}{
		"model": p.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": p.cfg.Prompt},
			{"role": "user", "content": fmt.Sprintf(
				"请基于下面原始识别文本进行优化、纠错、理解语气词并分析意图，返回最终优化结果（纯文本，不要解释）：\n\n%s", text,
			)},
		},
		"temperature": 0.2,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal body: %w", err)
	}

	endpoint := strings.TrimRight(p.cfg.URL, "/") + "/chat/completions"
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.cfg.Key)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("API returned no choices")
	}

	return strings.TrimSpace(result.Choices[0].Message.Content), nil
}
