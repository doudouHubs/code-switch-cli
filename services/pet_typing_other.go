//go:build !windows

package services

// 非 Windows 不监听当前窗口键盘事件，避免把“全局桌面监听”悄悄降级成
// 语义不同的实现；调用方可以记录明确 unsupported 诊断后继续运行宠物。
func NewPetKeyboardMonitor(_ func()) (PetKeyboardMonitor, error) {
	return nil, ErrPetKeyboardUnsupported
}
