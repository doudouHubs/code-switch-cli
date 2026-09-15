package services

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPetTrayIconCropsIdleSubjectIntoFixedCanvas(t *testing.T) {
	atlas := image.NewNRGBA(image.Rect(0, 0, 12, 10))
	for y := 3; y < 6; y++ {
		for x := 2; x < 6; x++ {
			atlas.SetNRGBA(x, y, color.NRGBA{R: 32, G: 192, B: 96, A: 255})
		}
	}
	frame := PetAtlasFrame{
		X: 2, Y: 2, Width: 8, Height: 7,
		SubjectBounds: PetAtlasBounds{X: 0, Y: 1, Width: 4, Height: 3},
	}
	asset := newTrayIconTestAsset(t, atlas, frame, true)

	encoded, err := buildPetTrayIcon(asset)
	if err != nil {
		t.Fatalf("buildPetTrayIcon() error = %v", err)
	}
	icon, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("decode generated tray icon: %v", err)
	}
	if got := icon.Bounds(); got.Dx() != petTrayIconSize || got.Dy() != petTrayIconSize {
		t.Fatalf("tray icon bounds = %v, want %dx%d", got, petTrayIconSize, petTrayIconSize)
	}
	if alpha := color.NRGBAModel.Convert(icon.At(0, 0)).(color.NRGBA).A; alpha != 0 {
		t.Fatalf("tray icon corner alpha = %d, want transparent padding", alpha)
	}
	if alpha := color.NRGBAModel.Convert(icon.At(16, 15)).(color.NRGBA).A; alpha == 0 {
		t.Fatal("tray icon center is transparent, want cropped subject")
	}
}

func TestBuildPetTrayIconRejectsInvalidManifestGeometry(t *testing.T) {
	atlas := image.NewNRGBA(image.Rect(0, 0, 12, 10))
	validFrame := PetAtlasFrame{
		X: 1, Y: 1, Width: 8, Height: 7,
		SubjectBounds: PetAtlasBounds{X: 1, Y: 1, Width: 4, Height: 3},
	}
	tests := []struct {
		name    string
		frame   PetAtlasFrame
		idle    bool
		atlasW  int
		atlasH  int
		wantErr bool
	}{
		{name: "missing idle", frame: validFrame, idle: false, atlasW: 12, atlasH: 10, wantErr: true},
		{name: "frame outside atlas", frame: PetAtlasFrame{X: 9, Y: 1, Width: 8, Height: 7, SubjectBounds: validFrame.SubjectBounds}, idle: true, atlasW: 12, atlasH: 10, wantErr: true},
		{name: "subject outside frame", frame: PetAtlasFrame{X: 1, Y: 1, Width: 8, Height: 7, SubjectBounds: PetAtlasBounds{X: 6, Y: 1, Width: 4, Height: 3}}, idle: true, atlasW: 12, atlasH: 10, wantErr: true},
		{name: "atlas size mismatch", frame: validFrame, idle: true, atlasW: 11, atlasH: 10, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			asset := newTrayIconTestAssetWithManifest(t, atlas, test.frame, test.idle, test.atlasW, test.atlasH)
			if _, err := buildPetTrayIcon(asset); (err != nil) != test.wantErr {
				t.Fatalf("buildPetTrayIcon() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestPetServiceGetTrayIconUsesActiveSkinOnly(t *testing.T) {
	root := t.TempDir()
	atlas := image.NewNRGBA(image.Rect(0, 0, 12, 10))
	frame := PetAtlasFrame{
		X: 1, Y: 1, Width: 8, Height: 7,
		SubjectBounds: PetAtlasBounds{X: 1, Y: 1, Width: 4, Height: 3},
	}
	asset := newTrayIconTestAsset(t, atlas, frame, true)
	atlasBytes, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(asset.Src, petAtlasDataURLPrefix))
	if err != nil {
		t.Fatalf("decode test atlas data URL: %v", err)
	}
	manifestPath := filepath.Join(root, "pet.json")
	if err := os.WriteFile(manifestPath, asset.Manifest, 0o600); err != nil {
		t.Fatalf("write test manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "atlas.png"), atlasBytes, 0o600); err != nil {
		t.Fatalf("write test atlas: %v", err)
	}

	activeSkinID := "custom"
	repository := &memoryPetRepository{snapshot: PetMigrationSnapshot{
		PetID:         DefaultPetID,
		Skins:         []PetSkinRecord{{PetID: DefaultPetID, SkinID: activeSkinID, Path: root}},
		SkinSelection: &PetSkinSelection{PetID: DefaultPetID, ActiveSkinID: &activeSkinID},
	}}
	service := NewPetService(repository)
	if icon, err := service.GetTrayIcon(DefaultPetID); err != nil {
		t.Fatalf("GetTrayIcon() error = %v", err)
	} else if decoded, decodeErr := png.Decode(bytes.NewReader(icon)); decodeErr != nil || decoded.Bounds().Dx() != petTrayIconSize {
		t.Fatalf("GetTrayIcon() output = %v, want decodable %dx%d PNG", decodeErr, petTrayIconSize, petTrayIconSize)
	}

	missingSkinID := "missing"
	repository.snapshot.SkinSelection.ActiveSkinID = &missingSkinID
	if _, err := service.GetTrayIcon(DefaultPetID); err == nil {
		t.Fatal("GetTrayIcon() succeeded for missing active skin, want fixed-fallback trigger error")
	}
}

func newTrayIconTestAsset(t *testing.T, atlas image.Image, frame PetAtlasFrame, idle bool) *PetAtlasAsset {
	t.Helper()
	return newTrayIconTestAssetWithManifest(t, atlas, frame, idle, atlas.Bounds().Dx(), atlas.Bounds().Dy())
}

func newTrayIconTestAssetWithManifest(t *testing.T, atlas image.Image, frame PetAtlasFrame, idle bool, width, height int) *PetAtlasAsset {
	t.Helper()
	var atlasBytes bytes.Buffer
	if err := png.Encode(&atlasBytes, atlas); err != nil {
		t.Fatalf("encode test atlas: %v", err)
	}
	manifest := PetAtlasManifest{
		AtlasVersion: PetAtlasVersion,
		Atlas: PetAtlasMetadata{
			AtlasVersion: PetAtlasVersion,
			Image:        "atlas.png",
			Width:        width,
			Height:       height,
		},
		Animations: map[string]PetAtlasAnimation{},
	}
	if idle {
		manifest.Animations["idle"] = PetAtlasAnimation{Frames: []PetAtlasFrame{frame}}
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal test manifest: %v", err)
	}
	return &PetAtlasAsset{
		Src:      petAtlasDataURLPrefix + base64.StdEncoding.EncodeToString(atlasBytes.Bytes()),
		Manifest: manifestBytes,
	}
}
