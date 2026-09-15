//go:build !windows

package services

import (
	"context"
	"net"
)

// 非 Windows 首期只要求公共 API 可编译。传输层不伪造 localhost fallback，避免
// 上层误以为已经具备与 Windows 命名管道相同的单用户隔离和宿主句柄语义。
func normalizePetLinkPipeName(name string) string {
	return name
}

func isPetLinkNotFound(_ error) bool {
	return false
}

func acquirePetLinkInstance(string) (func() error, error) {
	return func() error { return nil }, nil
}

func listenPetLink(string) (net.Listener, error) {
	return nil, ErrPetLinkUnsupported
}

func dialPetLink(context.Context, string) (net.Conn, error) {
	return nil, ErrPetLinkUnsupported
}

func newPetHostProcessMonitor(context.Context, PetHostProcessIdentity, func()) (*PetHostProcessMonitor, error) {
	return nil, ErrPetLinkUnsupported
}

func currentPetHostProcessIdentity() (PetHostProcessIdentity, error) {
	return PetHostProcessIdentity{}, ErrPetLinkUnsupported
}
