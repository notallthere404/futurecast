package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	v1 "github.com/notallthere404/futurecast/server/api/v1"

	"github.com/gofrs/uuid/v5"
)

// chatRequest matches the OpenAI chat-completions schema. Most
// providers (OpenAI, OpenRouter, Together, Groq, vLLM, Anthropic's
// compat endpoint) accept this exact shape.
type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	ResponseFormat map[string]any `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

// classifyAPI sends one chat-completion request per attribute to the
// configured remote endpoint. Each response is a JSON object mapping
// label name to confidence score, which we filter by cutoff and trim
// to top_n before returning in the same ClassifyResponse shape the
// self-hosted path produces.
func (c *Client) classifyAPI(ctx context.Context, art *v1.ClassifyArticle, spec []v1.ClassificationSpec, target Target) ([]*ClassifyResponse, error) {
	if target.Endpoint == "" || target.APIKey == "" {
		return nil, errors.New("api mode missing endpoint or api_key")
	}

	out := make([]*ClassifyResponse, 0, len(spec))
	for _, cls := range spec {
		data := make(map[string][]*v1.LabelScore, len(cls.Attributes))
		for _, attr := range cls.Attributes {
			scores, err := c.callChatCompletion(ctx, target, art.Content, attr)
			if err != nil {
				return nil, fmt.Errorf("classify %s.%s: %w", cls.Name, attr.Name, err)
			}
			data[attr.Name] = scores
		}
		out = append(out, &ClassifyResponse{
			Classification: cls.Name,
			ID:             uuid.Must(uuid.NewV4()).String(),
			ArticleID:      art.ID,
			Timestamp:      art.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
			Data:           data,
		})
	}
	return out, nil
}

// callChatCompletion fires one chat-completion against the remote
// endpoint for a single attribute. The prompt asks for a JSON object
// mapping each applicable label to a confidence score; the response
// gets filtered by attribute Cutoff and trimmed to TopN.
func (c *Client) callChatCompletion(ctx context.Context, target Target, content string, attr v1.AttributeSpec) ([]*v1.LabelScore, error) {
	body, err := json.Marshal(chatRequest{
		Model: target.Model,
		Messages: []chatMessage{
			{Role: "system", Content: buildClassifyPrompt(attr)},
			{Role: "user", Content: content},
		},
		ResponseFormat: map[string]any{"type": "json_object"},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(target.Endpoint, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+target.APIKey)

	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer res.Body.Close()

	respBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status %d: %s", res.StatusCode, truncate(respBody, 256))
	}

	var chat chatResponse
	if err := json.Unmarshal(respBody, &chat); err != nil {
		return nil, fmt.Errorf("unmarshal chat response: %w", err)
	}
	if chat.Error != nil {
		return nil, fmt.Errorf("remote error: %s", chat.Error.Message)
	}
	if len(chat.Choices) == 0 {
		return nil, errors.New("empty choices in response")
	}

	scores, err := parseLabelScores(chat.Choices[0].Message.Content)
	if err != nil {
		return nil, fmt.Errorf("parse model output: %w", err)
	}
	return filterAndTrim(scores, attr.Cutoff, attr.TopN), nil
}

// buildClassifyPrompt assembles the system message that asks the
// remote model to score labels. Definitions are included when set so
// the model has explicit semantics for ambiguous labels.
func buildClassifyPrompt(attr v1.AttributeSpec) string {
	var sb strings.Builder
	if attr.Instruction != "" {
		sb.WriteString(attr.Instruction)
		sb.WriteString("\n\n")
	}
	sb.WriteString("Available labels:\n")
	for _, l := range attr.Labels {
		if l.Definition != "" {
			fmt.Fprintf(&sb, "- %s: %s\n", l.Name, l.Definition)
			continue
		}
		fmt.Fprintf(&sb, "- %s\n", l.Name)
	}
	sb.WriteString("\nReturn a JSON object mapping each applicable label name to a confidence score from 0.0 to 1.0.\n")
	sb.WriteString(`Format: {"Label A": 0.85, "Label B": 0.4}` + "\n")
	if attr.Cutoff > 0 {
		fmt.Fprintf(&sb, "Only include labels with confidence at or above %.2f.\n", attr.Cutoff)
	}
	sb.WriteString("Return JSON only. No prose, no preamble, no trailing commentary.")
	return sb.String()
}

// parseLabelScores reads the model's JSON output into label/score
// pairs. Tolerates two common shapes: a flat object {"Label": 0.8} or
// an object wrapped under any top-level key (e.g. {"labels": {...}}).
// Also strips ```json fences that some models add despite asks.
func parseLabelScores(raw string) ([]*v1.LabelScore, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var generic map[string]any
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}

	if scores, ok := flattenScores(generic); ok {
		return scores, nil
	}
	if len(generic) == 1 {
		for _, v := range generic {
			if inner, ok := v.(map[string]any); ok {
				if scores, ok := flattenScores(inner); ok {
					return scores, nil
				}
			}
		}
	}
	return nil, errors.New("no label/score map found in response")
}

func flattenScores(m map[string]any) ([]*v1.LabelScore, bool) {
	if len(m) == 0 {
		return nil, false
	}
	out := make([]*v1.LabelScore, 0, len(m))
	for label, raw := range m {
		score, ok := toFloat(raw)
		if !ok {
			return nil, false
		}
		out = append(out, &v1.LabelScore{Label: label, Score: score})
	}
	return out, true
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	}
	return 0, false
}

// filterAndTrim drops scores below cutoff, sorts descending by score,
// trims to topN, and returns the n/i sentinel when nothing survives so
// the persisted row shape stays consistent with the self-hosted path.
func filterAndTrim(scores []*v1.LabelScore, cutoff float64, topN int) []*v1.LabelScore {
	out := make([]*v1.LabelScore, 0, len(scores))
	for _, s := range scores {
		if s.Score >= cutoff {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return []*v1.LabelScore{{Label: "n/i", Score: 0.0}}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if topN > 0 && len(out) > topN {
		out = out[:topN]
	}
	return out
}
