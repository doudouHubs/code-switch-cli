package main

import (
	"bytes"
	"image/color"
	"image/png"
	"testing"
)

func TestBuildFallbackPetTrayIconReturnsTransparentPNG(t *testing.T) {
	encoded := buildFallbackPetTrayIcon()
	icon, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("decode fallback tray icon: %v", err)
	}
	if got := icon.Bounds(); got.Dx() != fallbackPetTrayIconSize || got.Dy() != fallbackPetTrayIconSize {
		t.Fatalf("fallback tray icon bounds = %v, want %dx%d", got, fallbackPetTrayIconSize, fallbackPetTrayIconSize)
	}
	if alpha := color.NRGBAModel.Convert(icon.At(0, 0)).(color.NRGBA).A; alpha != 0 {
		t.Fatalf("fallback tray icon corner alpha = %d, want transparent background", alpha)
	}
	if alpha := color.NRGBAModel.Convert(icon.At(16, 16)).(color.NRGBA).A; alpha == 0 {
		t.Fatal("fallback tray icon center is transparent, want visible pet mark")
	}
}
