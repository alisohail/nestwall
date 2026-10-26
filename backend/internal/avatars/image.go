// Package avatars stores member profile pictures. Uploads are checked, cropped and
// re-encoded as 256 and 96 px JPEG squares, which also removes metadata such as EXIF.
package avatars

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"net/http"
)

// Upload and pixel limits.
const (
	MaxUploadBytes = 5 << 20
	maxSide        = 4000
	maxPixels      = 16_000_000
	LargeSize      = 256
	SmallSize      = 96
	jpegQuality    = 88
)

// Rejection reasons. The handlers map each one to a response code.
var (
	ErrUnsupportedType = errors.New("unsupported image type")
	ErrAnimated        = errors.New("animated images are not allowed")
	ErrDimensions      = errors.New("image dimensions are too large")
	ErrInvalidImage    = errors.New("image cannot be decoded")
	ErrInvalidCrop     = errors.New("crop is outside the image")
)

// Crop is a square region of the source picture in pixels. Size 0 means the largest centred square.
type Crop struct {
	X, Y, Size int
}

// Processed holds the two encoded sizes of one picture.
type Processed struct {
	Large []byte
	Small []byte
}

// Process checks the upload and returns the 256 and 96 px JPEG squares.
func Process(data []byte, crop Crop) (Processed, error) {
	var out Processed
	img, err := decode(data)
	if err != nil {
		return out, err
	}

	bounds := img.Bounds()
	rect, err := cropRect(bounds, crop)
	if err != nil {
		return out, err
	}

	if out.Large, err = encodeSquare(img, rect, LargeSize); err != nil {
		return out, err
	}
	if out.Small, err = encodeSquare(img, rect, SmallSize); err != nil {
		return out, err
	}
	return out, nil
}

// decode checks the type, the animation and the size, then decodes the picture.
func decode(data []byte) (image.Image, error) {
	if len(data) == 0 {
		return nil, ErrInvalidImage
	}
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	switch http.DetectContentType(head) {
	case "image/jpeg", "image/png":
	default:
		// SVG and other text formats are detected as text, so they land here too.
		return nil, ErrUnsupportedType
	}
	if isAnimatedPNG(data) {
		return nil, ErrAnimated
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalidImage
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxSide || cfg.Height > maxSide || cfg.Width*cfg.Height > maxPixels {
		return nil, ErrDimensions
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalidImage
	}
	return img, nil
}

// isAnimatedPNG reports whether the PNG has an animation control chunk (APNG) before its image data.
func isAnimatedPNG(data []byte) bool {
	const signature = "\x89PNG\r\n\x1a\n"
	if len(data) < 8 || string(data[:8]) != signature {
		return false
	}
	pos := 8
	for pos+8 <= len(data) {
		length := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		chunk := string(data[pos+4 : pos+8])
		switch chunk {
		case "acTL":
			return true
		case "IDAT":
			return false
		}
		next := pos + 12 + length
		if length < 0 || next > len(data) || next <= pos {
			return false
		}
		pos = next
	}
	return false
}

// cropRect returns the square to use. A zero crop size selects the largest centred square.
func cropRect(bounds image.Rectangle, crop Crop) (image.Rectangle, error) {
	w, h := bounds.Dx(), bounds.Dy()
	if crop.Size == 0 {
		side := min(w, h)
		x := bounds.Min.X + (w-side)/2
		y := bounds.Min.Y + (h-side)/2
		return image.Rect(x, y, x+side, y+side), nil
	}
	if crop.X < 0 || crop.Y < 0 || crop.Size < 1 || crop.X+crop.Size > w || crop.Y+crop.Size > h {
		return image.Rectangle{}, ErrInvalidCrop
	}
	x := bounds.Min.X + crop.X
	y := bounds.Min.Y + crop.Y
	return image.Rect(x, y, x+crop.Size, y+crop.Size), nil
}

// encodeSquare scales the square region to size x size, flattens transparency onto white and encodes JPEG.
func encodeSquare(src image.Image, r image.Rectangle, size int) ([]byte, error) {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	side := r.Dx()

	for dy := 0; dy < size; dy++ {
		y0, y1 := blockRange(r.Min.Y, side, size, dy)
		for dx := 0; dx < size; dx++ {
			x0, x1 := blockRange(r.Min.X, side, size, dx)
			dst.SetRGBA(dx, dy, averageBlock(src, x0, x1, y0, y1))
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// blockRange returns the source pixels [start, end) that feed destination pixel i.
func blockRange(origin, side, size, i int) (int, int) {
	start := origin + i*side/size
	end := origin + (i+1)*side/size
	if end <= start {
		end = start + 1
	}
	return start, end
}

// averageBlock averages the source pixels in the block over a white background.
func averageBlock(src image.Image, x0, x1, y0, y1 int) color.RGBA {
	var sr, sg, sb, n uint64
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, a := src.At(x, y).RGBA()
			// The values are premultiplied, so adding the white that shows through completes the pixel.
			sr += uint64(r + 0xffff - a)
			sg += uint64(g + 0xffff - a)
			sb += uint64(b + 0xffff - a)
			n++
		}
	}
	return color.RGBA{
		R: uint8(sr / n >> 8),
		G: uint8(sg / n >> 8),
		B: uint8(sb / n >> 8),
		A: 0xff,
	}
}
