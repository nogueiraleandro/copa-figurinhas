package qr

import (
	"encoding/base64"
	"fmt"

	qrcode "github.com/skip2/go-qrcode"
)

// GeneratePNG generates a QR code PNG for a URL and returns raw bytes.
func GeneratePNG(url string, size int) ([]byte, error) {
	png, err := qrcode.Encode(url, qrcode.Medium, size)
	if err != nil {
		return nil, fmt.Errorf("generate qr: %w", err)
	}
	return png, nil
}

// GenerateBase64 generates a QR code as a base64-encoded PNG (for embedding in HTML).
func GenerateBase64(url string, size int) (string, error) {
	png, err := GeneratePNG(url, size)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(png), nil
}

// StickerURL returns the URL for a participant's QR sticker.
func StickerURL(baseURL, token string) string {
	return fmt.Sprintf("%s/s/%s", baseURL, token)
}
