package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"
const defaultModel = "gemini-2.5-flash-image"

// Client calls the Gemini generateContent image endpoint.
type Client struct {
	APIKey  string
	Model   string
	BaseURL string
	HTTP    *http.Client
}

// StyleImage sends the source photo, optional style reference, and prompt to Gemini.
func (c *Client) StyleImage(ctx context.Context, photo []byte, photoMime string, ref []byte, refMime, prompt string) ([]byte, string, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return nil, "", fmt.Errorf("gemini api key vazia")
	}
	if len(photo) == 0 {
		return nil, "", fmt.Errorf("foto vazia")
	}
	if strings.TrimSpace(photoMime) == "" {
		photoMime = http.DetectContentType(photo)
	}
	model := strings.TrimSpace(c.Model)
	if model == "" {
		model = defaultModel
	}
	baseURL := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}

	parts := []map[string]interface{}{
		{"text": prompt},
		{"inline_data": map[string]string{
			"mime_type": photoMime,
			"data":      base64.StdEncoding.EncodeToString(photo),
		}},
	}
	if len(ref) > 0 {
		if strings.TrimSpace(refMime) == "" {
			refMime = http.DetectContentType(ref)
		}
		parts = append(parts, map[string]interface{}{
			"inline_data": map[string]string{
				"mime_type": refMime,
				"data":      base64.StdEncoding.EncodeToString(ref),
			},
		})
	}

	payload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{"parts": parts},
		},
		"generationConfig": map[string]interface{}{
			"responseModalities": []string{"IMAGE"},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}

	endpoint := fmt.Sprintf("%s/models/%s:generateContent?key=%s",
		baseURL, url.PathEscape(model), url.QueryEscape(c.APIKey))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, "", fmt.Errorf("gemini status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var decoded generateContentResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, "", err
	}
	for _, cand := range decoded.Candidates {
		for _, part := range cand.Content.Parts {
			inline := part.inline()
			if inline == nil || !strings.HasPrefix(inline.MimeType, "image/") || inline.Data == "" {
				continue
			}
			out, err := base64.StdEncoding.DecodeString(inline.Data)
			if err != nil {
				return nil, "", fmt.Errorf("imagem gemini invalida: %w", err)
			}
			return out, inline.MimeType, nil
		}
	}
	return nil, "", fmt.Errorf("gemini nao retornou imagem")
}

type inlineData struct {
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

type responsePart struct {
	InlineData      *inlineData `json:"inlineData"`
	InlineDataSnake *struct {
		Data     string `json:"data"`
		MimeType string `json:"mime_type"`
	} `json:"inline_data"`
}

func (p responsePart) inline() *inlineData {
	if p.InlineData != nil {
		return p.InlineData
	}
	if p.InlineDataSnake != nil {
		return &inlineData{Data: p.InlineDataSnake.Data, MimeType: p.InlineDataSnake.MimeType}
	}
	return nil
}

type generateContentResponse struct {
	Candidates []struct {
		Content struct {
			Parts []responsePart `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}
