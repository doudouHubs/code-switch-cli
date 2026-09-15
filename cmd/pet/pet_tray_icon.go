package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

const fallbackPetTrayIconSize = 32

// buildFallbackPetTrayIcon 返回与当前皮肤无关的固定宠物脸图标。兜底图标直接由
// 代码生成并随 EXE 固化，避免当前皮肤 atlas 损坏时连最后一个可见托盘入口也丢失。
func buildFallbackPetTrayIcon() []byte {
	icon := image.NewNRGBA(image.Rect(0, 0, fallbackPetTrayIconSize, fallbackPetTrayIconSize))

	// 先画耳朵再画脸，保证轮廓有稳定的前后层次；透明背景让 Windows 不会显示
	// 额外的方形底色，同时所有分辨率都能由系统按同一张 32x32 PNG 缩放。
	paintFallbackCircle(icon, 9, 9, 5, color.NRGBA{R: 143, G: 93, B: 54, A: 255})
	paintFallbackCircle(icon, 23, 9, 5, color.NRGBA{R: 143, G: 93, B: 54, A: 255})
	paintFallbackCircle(icon, 16, 16, 12, color.NRGBA{R: 190, G: 128, B: 75, A: 255})
	paintFallbackCircle(icon, 12, 15, 2, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	paintFallbackCircle(icon, 20, 15, 2, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	paintFallbackCircle(icon, 12, 15, 1, color.NRGBA{R: 43, G: 34, B: 30, A: 255})
	paintFallbackCircle(icon, 20, 15, 1, color.NRGBA{R: 43, G: 34, B: 30, A: 255})
	paintFallbackCircle(icon, 16, 20, 2, color.NRGBA{R: 75, G: 46, B: 32, A: 255})
	icon.SetNRGBA(15, 22, color.NRGBA{R: 75, G: 46, B: 32, A: 255})
	icon.SetNRGBA(16, 22, color.NRGBA{R: 75, G: 46, B: 32, A: 255})
	icon.SetNRGBA(17, 22, color.NRGBA{R: 75, G: 46, B: 32, A: 255})

	var output bytes.Buffer
	if err := png.Encode(&output, icon); err != nil {
		return nil
	}
	return output.Bytes()
}

func paintFallbackCircle(image *image.NRGBA, centerX, centerY, radius int, fill color.NRGBA) {
	for y := centerY - radius; y <= centerY+radius; y++ {
		for x := centerX - radius; x <= centerX+radius; x++ {
			deltaX := x - centerX
			deltaY := y - centerY
			if deltaX*deltaX+deltaY*deltaY <= radius*radius && x >= 0 && y >= 0 && x < fallbackPetTrayIconSize && y < fallbackPetTrayIconSize {
				image.SetNRGBA(x, y, fill)
			}
		}
	}
}
