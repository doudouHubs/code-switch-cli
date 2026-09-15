//go:build windows

package main

import (
	"context"
	"testing"
	"time"

	"codeswitch/services"
)

func TestNewPetProcessManagerReusesExistingPetWithoutExecutable(t *testing.T) {
	server, err := services.NewPetLinkServer(services.PetLinkServerOptions{})
	if err != nil {
		t.Fatalf("NewPetLinkServer() error = %v", err)
	}
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("PetLinkServer.Start() error = %v", err)
	}
	defer server.Close()

	// 临时工作目录确保 findPetExecutable 不会命中仓库中的发布产物，
	// 这样测试验证的是“已有管道优先”，而不是 EXE 路径搜索的副作用。
	t.Chdir(t.TempDir())
	t.Setenv(petProcessModeEnv, "")
	t.Setenv(petProcessExeEnv, "")

	manager, err := newPetProcessManager()
	if err != nil {
		t.Fatalf("newPetProcessManager() error = %v", err)
	}
	if !manager.Enabled() {
		t.Fatal("newPetProcessManager() selected embedded mode while a Pet instance is running")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("petProcessManager.Start() error = %v", err)
	}
	if !manager.Linked() {
		t.Fatal("petProcessManager is not linked after reusing the existing Pet")
	}
	if err := manager.Stop(ctx); err != nil {
		t.Fatalf("petProcessManager.Stop() error = %v", err)
	}
}
