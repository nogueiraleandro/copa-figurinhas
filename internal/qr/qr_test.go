package qr

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"strings"
	"testing"
)

func TestStickerURL(t *testing.T) {
	got := StickerURL("http://192.168.0.10:8080", "abc123")
	want := "http://192.168.0.10:8080/s/abc123"
	if got != want {
		t.Fatalf("StickerURL = %q, want %q", got, want)
	}
}

func TestGeneratePNGIsValidImage(t *testing.T) {
	data, err := GeneratePNG("http://192.168.0.10:8080/s/abc123", 256)
	if err != nil {
		t.Fatalf("GeneratePNG: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("PNG vazio")
	}
	// Deve decodificar como PNG valido.
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("PNG invalido: %v", err)
	}
}

func TestGenerateBase64Decodes(t *testing.T) {
	b64, err := GenerateBase64("http://192.168.0.10:8080/s/abc123", 256)
	if err != nil {
		t.Fatalf("GenerateBase64: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 invalido: %v", err)
	}
	if !strings.HasPrefix(string(raw[1:4]), "PNG") {
		t.Fatalf("conteudo decodificado nao parece PNG")
	}
}
