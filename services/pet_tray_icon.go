package services

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	imagedraw "image/draw"
	"image/png"
	"strings"

	xdraw "golang.org/x/image/draw"
)

const (
	petTrayIconSize        = 32
	petTrayIconContentSize = 28
)

// GetTrayIcon 读取当前激活皮肤的 idle 首帧，并生成 Windows 托盘需要的小尺寸 PNG。
// 这个入口只服务于原生托盘，不复用 resolvePetAtlas 的跨皮肤 fallback：当前皮肤
// 损坏时必须把错误交给调用方，由调用方切换固定兜底图标，不能悄悄显示另一套皮肤。
func (s *PetService) GetTrayIcon(petID string) ([]byte, error) {
	service, err := s.apiServiceForPet(petID)
	if err != nil {
		return nil, err
	}

	snapshot, err := service.Load()
	if err != nil {
		return nil, err
	}
	asset, err := resolveActivePetAtlas(snapshot)
	if err != nil {
		return nil, fmt.Errorf("读取当前宠物皮肤托盘资源: %w", err)
	}
	return buildPetTrayIcon(asset)
}

func resolveActivePetAtlas(snapshot PetMigrationSnapshot) (*PetAtlasAsset, error) {
	activeSkinID := builtinPetSkinIDs[0]
	if snapshot.SkinSelection != nil && snapshot.SkinSelection.ActiveSkinID != nil {
		if value := strings.TrimSpace(*snapshot.SkinSelection.ActiveSkinID); value != "" {
			activeSkinID = value
		}
	}

	for _, skin := range snapshot.Skins {
		if strings.TrimSpace(skin.SkinID) != activeSkinID {
			continue
		}
		asset, err := loadPetSkinAtlas(skin)
		if err != nil {
			return nil, err
		}
		return asset, nil
	}

	asset, err := loadBuiltinPetAtlas(activeSkinID)
	if err != nil {
		return nil, fmt.Errorf("当前皮肤 %q 不存在或资源损坏: %w", activeSkinID, err)
	}
	return asset, nil
}

func buildPetTrayIcon(asset *PetAtlasAsset) ([]byte, error) {
	if asset == nil {
		return nil, errors.New("托盘 atlas 为空")
	}

	var manifest PetAtlasManifest
	manifestBytes := bytes.TrimSpace(bytes.TrimPrefix(asset.Manifest, []byte{0xEF, 0xBB, 0xBF}))
	if len(manifestBytes) == 0 {
		return nil, errors.New("托盘皮肤 manifest 为空")
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("解析托盘皮肤 manifest: %w", err)
	}
	if manifest.AtlasVersion != PetAtlasVersion {
		return nil, errors.New("托盘皮肤 manifest atlasVersion 不受支持")
	}

	idle, ok := manifest.Animations["idle"]
	if !ok || len(idle.Frames) == 0 {
		return nil, errors.New("托盘皮肤缺少 idle 首帧")
	}
	frame := idle.Frames[0]
	atlasBounds := image.Rect(0, 0, manifest.Atlas.Width, manifest.Atlas.Height)
	frameBounds, err := petTrayFrameBounds(frame, atlasBounds)
	if err != nil {
		return nil, err
	}
	subjectBounds, err := petTraySubjectBounds(frame, frameBounds)
	if err != nil {
		return nil, err
	}

	atlasBytes, err := decodePetTrayAtlas(asset.Src)
	if err != nil {
		return nil, err
	}
	atlas, err := png.Decode(bytes.NewReader(atlasBytes))
	if err != nil {
		return nil, fmt.Errorf("解码托盘皮肤 atlas: %w", err)
	}
	if atlas.Bounds().Dx() != manifest.Atlas.Width || atlas.Bounds().Dy() != manifest.Atlas.Height {
		return nil, errors.New("托盘皮肤 atlas 尺寸与 manifest 不一致")
	}

	// 只裁剪 subjectBounds，去掉 atlas 行布局和透明留白；再放入固定尺寸画布，
	// 防止不同宽高比的皮肤让 Windows 托盘图标在刷新时发生尺寸跳变。
	subject := image.NewNRGBA(image.Rect(0, 0, subjectBounds.Dx(), subjectBounds.Dy()))
	imagedraw.Draw(subject, subject.Bounds(), atlas, subjectBounds.Min, imagedraw.Src)

	width, height := fitPetTrayContent(subject.Bounds().Dx(), subject.Bounds().Dy())
	left := (petTrayIconSize - width) / 2
	top := (petTrayIconSize - height) / 2
	destination := image.Rect(left, top, left+width, top+height)
	icon := image.NewNRGBA(image.Rect(0, 0, petTrayIconSize, petTrayIconSize))
	xdraw.CatmullRom.Scale(icon, destination, subject, subject.Bounds(), xdraw.Over, nil)

	var output bytes.Buffer
	if err := png.Encode(&output, icon); err != nil {
		return nil, fmt.Errorf("编码托盘图标: %w", err)
	}
	return output.Bytes(), nil
}

func decodePetTrayAtlas(src string) ([]byte, error) {
	if !strings.HasPrefix(src, petAtlasDataURLPrefix) {
		return nil, errors.New("托盘 atlas 不是受控 PNG data URL")
	}
	encoded := strings.TrimSpace(strings.TrimPrefix(src, petAtlasDataURLPrefix))
	if encoded == "" {
		return nil, errors.New("托盘 atlas data URL 为空")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("解码托盘 atlas data URL: %w", err)
	}
	return data, nil
}

func petTrayFrameBounds(frame PetAtlasFrame, atlasBounds image.Rectangle) (image.Rectangle, error) {
	if frame.X < 0 || frame.Y < 0 || frame.Width <= 0 || frame.Height <= 0 {
		return image.Rectangle{}, errors.New("托盘皮肤 idle 首帧尺寸无效")
	}
	if frame.X > atlasBounds.Dx() || frame.Y > atlasBounds.Dy() ||
		frame.Width > atlasBounds.Dx()-frame.X || frame.Height > atlasBounds.Dy()-frame.Y {
		return image.Rectangle{}, errors.New("托盘皮肤 idle 首帧超出 atlas 边界")
	}
	return image.Rect(frame.X, frame.Y, frame.X+frame.Width, frame.Y+frame.Height), nil
}

func petTraySubjectBounds(frame PetAtlasFrame, frameBounds image.Rectangle) (image.Rectangle, error) {
	subject := frame.SubjectBounds
	if subject.X < 0 || subject.Y < 0 || subject.Width <= 0 || subject.Height <= 0 {
		return image.Rectangle{}, errors.New("托盘皮肤 subjectBounds 无效")
	}
	if subject.X > frameBounds.Dx() || subject.Y > frameBounds.Dy() ||
		subject.Width > frameBounds.Dx()-subject.X || subject.Height > frameBounds.Dy()-subject.Y {
		return image.Rectangle{}, errors.New("托盘皮肤 subjectBounds 超出首帧边界")
	}
	return image.Rect(
		frameBounds.Min.X+subject.X,
		frameBounds.Min.Y+subject.Y,
		frameBounds.Min.X+subject.X+subject.Width,
		frameBounds.Min.Y+subject.Y+subject.Height,
	), nil
}

func fitPetTrayContent(width, height int) (int, int) {
	if width <= 0 || height <= 0 {
		return 1, 1
	}
	if width >= height {
		scaledHeight := int(int64(petTrayIconContentSize) * int64(height) / int64(width))
		if scaledHeight < 1 {
			scaledHeight = 1
		}
		return petTrayIconContentSize, scaledHeight
	}
	scaledWidth := int(int64(petTrayIconContentSize) * int64(width) / int64(height))
	if scaledWidth < 1 {
		scaledWidth = 1
	}
	return scaledWidth, petTrayIconContentSize
}
