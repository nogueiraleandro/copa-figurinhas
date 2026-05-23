package handler

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"strings"

	_ "image/gif" // permite decodificar GIF de entrada

	xdraw "golang.org/x/image/draw"
)

const maxImageDim = 600 // lado maximo, em px — figurinhas leves para o Wi-Fi local

// processImage reduz a imagem para no maximo maxImageDim de lado (se maior) e a
// reencoda. PNG (com transparencia) permanece PNG; o resto vira JPEG q=85.
// Retorna os bytes processados e a extensao (".png"/".jpg"). Se nao conseguir
// decodificar (formato desconhecido), devolve os bytes originais e a extensao dada.
func processImage(data []byte, origExt string) ([]byte, string) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, normalizeExt(origExt)
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > maxImageDim || h > maxImageDim {
		nw, nh := w, h
		if w >= h {
			nw = maxImageDim
			nh = h * maxImageDim / w
		} else {
			nh = maxImageDim
			nw = w * maxImageDim / h
		}
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
		img = dst
	}

	var out bytes.Buffer
	if format == "png" {
		if err := png.Encode(&out, img); err != nil {
			return data, normalizeExt(origExt)
		}
		return out.Bytes(), ".png"
	}
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 85}); err != nil {
		return data, normalizeExt(origExt)
	}
	return out.Bytes(), ".jpg"
}

func normalizeExt(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext == "" {
		return ".jpg"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return ext
}
