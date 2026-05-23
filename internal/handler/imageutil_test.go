package handler

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 100, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// Imagem grande deve ser reduzida para no maximo maxImageDim de lado.
func TestProcessImageShrinksLarge(t *testing.T) {
	src := makePNG(t, 1200, 900)
	out, ext := processImage(src, ".png")
	if ext != ".png" {
		t.Fatalf("PNG deveria continuar PNG, got %q", ext)
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("saida nao decodifica: %v", err)
	}
	b := img.Bounds()
	if b.Dx() > maxImageDim || b.Dy() > maxImageDim {
		t.Fatalf("imagem nao foi reduzida: %dx%d", b.Dx(), b.Dy())
	}
	// Proporcao preservada (1200x900 -> 600x450).
	if b.Dx() != maxImageDim || b.Dy() != 450 {
		t.Fatalf("dimensoes inesperadas: %dx%d (esperado 600x450)", b.Dx(), b.Dy())
	}
}

// Imagem pequena nao deve crescer.
func TestProcessImageKeepsSmall(t *testing.T) {
	src := makePNG(t, 200, 150)
	out, _ := processImage(src, ".png")
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if img.Bounds().Dx() != 200 || img.Bounds().Dy() != 150 {
		t.Fatalf("imagem pequena nao deveria mudar de tamanho, got %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

// Bytes que nao sao imagem: retorna original sem quebrar.
func TestProcessImageInvalidPassesThrough(t *testing.T) {
	junk := []byte("isto nao e uma imagem")
	out, ext := processImage(junk, ".png")
	if !bytes.Equal(out, junk) {
		t.Fatal("bytes invalidos deveriam passar inalterados")
	}
	if ext != ".png" {
		t.Fatalf("ext deveria ser normalizada para .png, got %q", ext)
	}
}
