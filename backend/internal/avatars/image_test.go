package avatars

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// pngBytes encodes a w x h test picture. With alpha set, the left half is transparent.
func pngBytes(t *testing.T, w, h int, alpha bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{R: uint8(x), G: uint8(y), B: 128, A: 255}
			if alpha && x < w/2 {
				c.A = 0
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// animatedPNG inserts an acTL chunk before the first IDAT chunk, which makes it an APNG.
func animatedPNG(t *testing.T) []byte {
	t.Helper()
	data := pngBytes(t, 20, 20, false)
	idat := bytes.Index(data, []byte("IDAT")) - 4
	acTL := []byte{0, 0, 0, 8, 'a', 'c', 'T', 'L', 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	out := append([]byte{}, data[:idat]...)
	out = append(out, acTL...)
	return append(out, data[idat:]...)
}

func decodedSize(t *testing.T, data []byte) image.Point {
	t.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("format = %s, want jpeg", format)
	}
	return image.Pt(cfg.Width, cfg.Height)
}

func TestProcessProducesSquareJPEGs(t *testing.T) {
	out, err := Process(pngBytes(t, 300, 200, false), Crop{})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got := decodedSize(t, out.Large); got != image.Pt(LargeSize, LargeSize) {
		t.Errorf("large = %v, want %dx%d", got, LargeSize, LargeSize)
	}
	if got := decodedSize(t, out.Small); got != image.Pt(SmallSize, SmallSize) {
		t.Errorf("small = %v, want %dx%d", got, SmallSize, SmallSize)
	}
}

func TestProcessUsesCropAndFlattensTransparencyOnWhite(t *testing.T) {
	// The left half of the source is transparent. The crop starts at x=0, so the top-left of the output is transparent.
	out, err := Process(pngBytes(t, 200, 200, true), Crop{X: 0, Y: 0, Size: 100})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	img, _, err := image.Decode(bytes.NewReader(out.Large))
	if err != nil {
		t.Fatalf("decode large: %v", err)
	}
	r, g, b, _ := img.At(10, 10).RGBA()
	for _, v := range []uint32{r >> 8, g >> 8, b >> 8} {
		if v < 240 {
			t.Fatalf("transparent area is not white: got channel %d", v)
		}
	}
}

func TestProcessRejectsBadCrop(t *testing.T) {
	_, err := Process(pngBytes(t, 200, 200, false), Crop{X: 150, Y: 0, Size: 100})
	if !errors.Is(err, ErrInvalidCrop) {
		t.Fatalf("err = %v, want ErrInvalidCrop", err)
	}
}

func TestProcessRejectsUnsupportedAndUnsafeInput(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"empty", nil, ErrInvalidImage},
		{"plain text", []byte("this is not a picture"), ErrUnsupportedType},
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"></svg>`), ErrUnsupportedType},
		{"animated png", animatedPNG(t), ErrAnimated},
		{"too wide", pngBytes(t, maxSide+1, 10, false), ErrDimensions},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Process(tc.data, Crop{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestIsAnimatedPNGOnlyFlagsAnimation(t *testing.T) {
	if isAnimatedPNG(pngBytes(t, 4, 4, false)) {
		t.Errorf("still PNG flagged as animated")
	}
	if !isAnimatedPNG(animatedPNG(t)) {
		t.Errorf("animated PNG not flagged")
	}
}
