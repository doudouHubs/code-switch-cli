//go:build windows

package services

import (
	"bufio"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/google/uuid"
)

func TestPetProcessSupervisorProbeReportsMissingInstance(t *testing.T) {
	pipeName := `\\.\pipe\codeswitch-pet-probe-missing-` + uuid.NewString()
	supervisor, err := NewPetProcessSupervisor(PetProcessOptions{PipeName: pipeName, StartTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewPetProcessSupervisor() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := supervisor.Probe(ctx); !errors.Is(err, ErrPetLinkNotFound) {
		t.Fatalf("Probe() error = %v, want ErrPetLinkNotFound", err)
	}
	if err := supervisor.Start(ctx); !errors.Is(err, ErrPetLinkNotFound) {
		t.Fatalf("Start() error = %v, want ErrPetLinkNotFound without executable", err)
	}
}

func TestPetProcessSupervisorProbeRejectsInvalidProtocol(t *testing.T) {
	pipeName := `\\.\pipe\codeswitch-pet-probe-invalid-` + uuid.NewString()
	listener, err := winio.ListenPipe(pipeName, &winio.PipeConfig{MessageMode: false})
	if err != nil {
		t.Fatalf("winio.ListenPipe() error = %v", err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		if _, readErr := reader.ReadBytes('\n'); readErr != nil {
			serverDone <- readErr
			return
		}
		_, writeErr := conn.Write([]byte(`{"protocol":"invalid","version":1,"id":"probe","ok":true}` + "\n"))
		serverDone <- writeErr
	}()

	supervisor, err := NewPetProcessSupervisor(PetProcessOptions{PipeName: pipeName})
	if err != nil {
		t.Fatalf("NewPetProcessSupervisor() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	probeErr := supervisor.Probe(ctx)
	if probeErr == nil {
		t.Fatal("Probe() unexpectedly accepted invalid protocol")
	}
	if errors.Is(probeErr, ErrPetLinkNotFound) {
		t.Fatalf("Probe() classified invalid protocol as missing instance: %v", probeErr)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("invalid protocol server error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("invalid protocol server did not finish")
	}
}

func TestPetLinkNamedPipeSupervisorRoundTrip(t *testing.T) {
	pipeName := `\\.\pipe\codeswitch-pet-test-` + uuid.NewString()
	server, err := NewPetLinkServer(PetLinkServerOptions{
		PipeName: pipeName,
		Handler: func(_ context.Context, request PetLinkRequest) (any, error) {
			return map[string]any{"method": request.Method, "params": request.Params}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewPetLinkServer() error = %v", err)
	}
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("PetLinkServer.Start() error = %v", err)
	}
	defer server.Close()

	supervisor, err := NewPetProcessSupervisor(PetProcessOptions{
		PipeName: pipeName,
		// 已有实例必须优先复用；如果 Start 先无条件启动，这个故意不存在的
		// 路径会让测试直接失败。
		Executable:   "pet-executable-is-not-needed.exe",
		StartTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPetProcessSupervisor() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := supervisor.Start(ctx); err != nil {
		t.Fatalf("PetProcessSupervisor.Start() error = %v", err)
	}
	if !supervisor.Linked() {
		t.Fatal("supervisor is not linked after Attach")
	}

	var result map[string]any
	if err := supervisor.Call(ctx, "pet.state", map[string]string{"petId": "default"}, &result); err != nil {
		t.Fatalf("supervisor.Call() error = %v", err)
	}
	if result["method"] != "pet.state" {
		t.Fatalf("Call() result = %#v", result)
	}
	if err := supervisor.Stop(ctx); err != nil {
		t.Fatalf("supervisor.Stop() error = %v", err)
	}
	if supervisor.Linked() {
		t.Fatal("supervisor remains linked after Stop")
	}
}

func TestPetLinkServerRejectsSecondInstance(t *testing.T) {
	pipeName := `\\.\pipe\codeswitch-pet-singleton-` + uuid.NewString()
	first, err := NewPetLinkServer(PetLinkServerOptions{PipeName: pipeName})
	if err != nil {
		t.Fatalf("NewPetLinkServer(first) error = %v", err)
	}
	if err := first.Start(context.Background()); err != nil {
		t.Fatalf("first.Start() error = %v", err)
	}
	defer first.Close()

	second, err := NewPetLinkServer(PetLinkServerOptions{PipeName: pipeName})
	if err != nil {
		t.Fatalf("NewPetLinkServer(second) error = %v", err)
	}
	if err := second.Start(context.Background()); !errors.Is(err, ErrPetLinkAlreadyRunning) {
		t.Fatalf("second.Start() error = %v, want ErrPetLinkAlreadyRunning", err)
	}
}

func TestPetHostProcessMonitorRejectsPetProcessIdentity(t *testing.T) {
	identity, err := CurrentPetHostProcessIdentity()
	if err != nil {
		t.Fatalf("CurrentPetHostProcessIdentity() error = %v", err)
	}
	if _, err := WatchPetHostProcess(context.Background(), identity, nil); err == nil {
		t.Fatal("WatchPetHostProcess() accepted the pet process as its own host")
	}
}
