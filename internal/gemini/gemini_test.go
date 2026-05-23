package gemini

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStyleImageDecodesInlineImage(t *testing.T) {
	img := []byte{0x89, 0x50, 0x4e, 0x47}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models/gemini-2.5-flash-image:generateContent" {
			t.Fatalf("path inesperado: %s", r.URL.Path)
		}
		if r.URL.Query().Get("key") != "key-123" {
			t.Fatalf("api key nao veio na query")
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "responseModalities") || !strings.Contains(string(body), "inline_data") {
			t.Fatalf("payload nao contem partes esperadas: %s", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"`+
			base64.StdEncoding.EncodeToString(img)+`"}}]}}]}`)
	}))
	defer srv.Close()

	client := &Client{APIKey: "key-123", BaseURL: srv.URL}
	out, mime, err := client.StyleImage(context.Background(), []byte("photo"), "image/jpeg", nil, "", "prompt")
	if err != nil {
		t.Fatalf("StyleImage: %v", err)
	}
	if mime != "image/png" || string(out) != string(img) {
		t.Fatalf("imagem decodificada errada: mime=%s bytes=%v", mime, out)
	}
}

func TestStyleImageErrorsOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "limite excedido", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client := &Client{APIKey: "key", BaseURL: srv.URL}
	_, _, err := client.StyleImage(context.Background(), []byte("photo"), "image/jpeg", nil, "", "prompt")
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("esperava erro com status, got %v", err)
	}
}

func TestStyleImageErrorsWithoutImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"sem imagem"}]}}]}`)
	}))
	defer srv.Close()

	client := &Client{APIKey: "key", BaseURL: srv.URL}
	_, _, err := client.StyleImage(context.Background(), []byte("photo"), "image/jpeg", nil, "", "prompt")
	if err == nil || !strings.Contains(err.Error(), "nao retornou imagem") {
		t.Fatalf("esperava erro sem imagem, got %v", err)
	}
}
