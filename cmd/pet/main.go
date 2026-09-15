package main

import (
	"context"
	"embed"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"codeswitch/services"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// 独立发行包只嵌入桌宠页面和资源，不把主程序的其它路由、模型配置或频道资源
// 带进来；PetWindow.vue 通过 appView=pet 直接作为透明窗口根组件挂载。
//
//go:embed all:dist all:resources/pets
var assets embed.FS

const (
	petActionMethod          = "pet.action"
	petSnapshotMethod        = "pet.snapshot"
	petSettingsMethod        = "pet.settings"
	petSaveSettingsMethod    = "pet.settings.save"
	petNameMethod            = "pet.name"
	petPettedMethod          = "pet.petted"
	petWindowOpenMethod      = "pet.window.open"
	petWindowCloseMethod     = "pet.window.close"
	petWindowToggleMethod    = "pet.window.toggle"
	petWindowStateMethod     = "pet.window.state"
	petWindowModeMethod      = "pet.window.mode"
	petWindowMoveMethod      = "pet.window.move"
	petWindowIdleMethod      = "pet.window.idle"
	petWindowResizeMethod    = "pet.window.resize"
	petWindowFocusMethod     = "pet.window.focus"
	petWindowTopmostMethod   = "pet.window.topmost"
	petWindowLayerMethod     = "pet.window.layer"
	petWindowPlatformsMethod = "pet.window.platforms"
)

type petRequestParams struct {
	PetID       string                    `json:"petId"`
	Action      services.PetAction        `json:"action"`
	Name        string                    `json:"name"`
	Enabled     *bool                     `json:"enabled,omitempty"`
	Settings    services.PetSettingsInput `json:"settings,omitempty"`
	Mode        string                    `json:"mode,omitempty"`
	X           int                       `json:"x,omitempty"`
	Y           int                       `json:"y,omitempty"`
	Width       int                       `json:"width,omitempty"`
	Height      int                       `json:"height,omitempty"`
	AlwaysOnTop bool                      `json:"alwaysOnTop,omitempty"`
	PlatformID  string                    `json:"platformId,omitempty"`
}

func main() {
	services.ConfigureRuntimeCrashOutput()
	services.SetPetAssetSource(assets)
	frontendAssets, err := fs.Sub(assets, "dist")
	if err != nil {
		log.Fatalf("读取独立宠物前端资源失败: %v", err)
	}

	db, err := services.InitPetStandaloneDatabase()
	if err != nil {
		log.Fatalf("初始化独立宠物数据库失败: %v", err)
	}
	defer db.Close()

	petDAO := services.NewPetDAO(db)
	if report, migrationErr := services.MigrateOpenCoworkPet(context.Background(), petDAO, services.PetMigrationOptions{}); migrationErr != nil {
		log.Printf("⚠️ OpenCowork 宠物迁移失败，保留目标默认配置: %v", migrationErr)
	} else if !report.AlreadyApplied {
		log.Printf("✅ OpenCowork 宠物迁移完成：导入=%d 跳过=%d 缺失=%d 失败=%d", report.Imported, report.Skipped, report.Missing, report.Failed)
	}
	petService := services.NewPetService(petDAO)

	var app *application.App
	var petWindow *services.PetWindow
	var petWindowAPI *services.PetWindowAPI
	var petKeyboardMonitor services.PetKeyboardMonitor
	var stopOnce sync.Once
	stopHeartbeat := make(chan struct{})
	petTypingController := services.NewPetTypingController(services.PetTypingControllerOptions{
		Emit: func(state services.PetTypingState) {
			if app != nil {
				app.Event.Emit(services.PetTypingEventName, state)
			}
		},
	})

	lifecycle := services.NewPetLinkLifecycle()
	runtime := services.NewPetRuntime(petService, services.PetRuntimeOptions{})
	var linkServer *services.PetLinkServer
	// handler 只暴露主程序联动所需的窄方法；复杂 AI/项目能力仍由主程序自己提供，
	// 防止独立桌宠进程意外变成一个通用本地 RPC 服务。
	linkHandler := func(ctx context.Context, request services.PetLinkRequest) (any, error) {
		var params petRequestParams
		if len(request.Params) > 0 {
			if err := json.Unmarshal(request.Params, &params); err != nil {
				return nil, fmt.Errorf("decode pet request params: %w", err)
			}
		}
		petID := strings.TrimSpace(params.PetID)
		if petID == "" {
			petID = services.DefaultPetID
		}
		switch request.Method {
		case petSnapshotMethod:
			return petService.GetRuntimeSnapshot(petID)
		case petSettingsMethod:
			return petService.GetSettingsSnapshot(petID)
		case petActionMethod:
			return petService.PerformAction(petID, params.Action)
		case petPettedMethod:
			return petService.PettedForPet(petID)
		case petNameMethod:
			return petService.UpdateName(petID, params.Name)
		case petSaveSettingsMethod:
			return petService.SaveSettings(petID, params.Settings)
		case petWindowOpenMethod:
			if petWindowAPI == nil {
				return nil, errors.New("pet window api is unavailable")
			}
			return nil, petWindowAPI.Open()
		case petWindowCloseMethod:
			if petWindowAPI == nil {
				return nil, errors.New("pet window api is unavailable")
			}
			return nil, petWindowAPI.Close()
		case petWindowToggleMethod:
			if petWindowAPI == nil {
				return nil, errors.New("pet window api is unavailable")
			}
			return nil, petWindowAPI.Toggle()
		case petWindowStateMethod:
			if petWindowAPI == nil {
				return nil, errors.New("pet window api is unavailable")
			}
			return petWindowAPI.State(), nil
		case petWindowModeMethod:
			if petWindowAPI == nil {
				return nil, errors.New("pet window api is unavailable")
			}
			return nil, petWindowAPI.SetMode(params.Mode)
		case petWindowMoveMethod:
			if petWindowAPI == nil {
				return nil, errors.New("pet window api is unavailable")
			}
			return nil, petWindowAPI.Move(params.X, params.Y)
		case petWindowIdleMethod:
			if petWindowAPI == nil {
				return nil, errors.New("pet window api is unavailable")
			}
			return petWindowAPI.IdleSeconds()
		case petWindowResizeMethod:
			if petWindowAPI == nil {
				return nil, errors.New("pet window api is unavailable")
			}
			return nil, petWindowAPI.Resize(params.Width, params.Height)
		case petWindowFocusMethod:
			if petWindowAPI == nil {
				return nil, errors.New("pet window api is unavailable")
			}
			return nil, petWindowAPI.Focus()
		case petWindowTopmostMethod:
			if petWindowAPI == nil {
				return nil, errors.New("pet window api is unavailable")
			}
			return nil, petWindowAPI.SetAlwaysOnTop(params.AlwaysOnTop)
		case petWindowLayerMethod:
			if petWindowAPI == nil {
				return nil, errors.New("pet window api is unavailable")
			}
			return nil, petWindowAPI.SetPlatformLayer(params.PlatformID)
		case petWindowPlatformsMethod:
			if petWindowAPI == nil {
				return nil, errors.New("pet window api is unavailable")
			}
			return petWindowAPI.GetPlatforms()
		default:
			return nil, fmt.Errorf("unsupported pet link method %q", request.Method)
		}
	}
	app = application.New(application.Options{
		Name:        "CodeSwitch Pet",
		Description: "Independent CodeSwitch desktop pet",
		Windows: application.WindowsOptions{
			WndProcInterceptor: services.PetWindowWndProcInterceptor,
		},
		Services: []application.Service{
			application.NewService(petService),
		},
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(frontendAssets)},
		Mac:    application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
	})
	petWindow, err = services.NewPetWindow(app, services.PetWindowOptions{
		Name:   services.DefaultPetWindowName,
		Title:  services.DefaultPetWindowTitle,
		URL:    "/?appView=pet",
		Width:  services.DefaultPetWindowWidth,
		Height: services.DefaultPetWindowHeight,
	})
	if err != nil {
		log.Fatalf("创建独立宠物窗口失败: %v", err)
	}
	petWindowAPI = services.NewPetWindowAPI(petWindow)
	app.RegisterService(application.NewService(petWindowAPI))
	app.RegisterService(application.NewService(services.NewPetTypingAPI(petTypingController)))

	linkServer, err = services.NewPetLinkServer(services.PetLinkServerOptions{
		Handler:   linkHandler,
		Lifecycle: lifecycle,
		OnHostExit: func() {
			if app != nil {
				go app.Quit()
			}
		},
		OnShutdown: func() error {
			if app != nil {
				go app.Quit()
			}
			return nil
		},
	})
	if err != nil {
		log.Fatalf("创建业务 link server 失败: %v", err)
	}
	if err := linkServer.Start(context.Background()); err != nil {
		log.Fatalf("启动业务 link server 失败: %v", err)
	}
	defer linkServer.Close()

	var keyboardErr error
	petKeyboardMonitor, keyboardErr = services.NewPetKeyboardMonitor(petTypingController.NotifyKey)
	if keyboardErr != nil {
		// 全局键盘监听是工作动画增强能力；权限或平台不支持时保留宠物
		// 主流程，前端通过 State() 得到 inactive，不把增强失败升级成启动失败。
		log.Printf("⚠️ 全局键盘监听不可用: %v", keyboardErr)
	} else if keyboardErr = petKeyboardMonitor.Start(); keyboardErr != nil {
		log.Printf("⚠️ 启动全局键盘监听失败: %v", keyboardErr)
		petKeyboardMonitor = nil
	}

	windowConfig, err := petService.GetWindowConfig()
	if err != nil {
		log.Printf("⚠️ 读取宠物窗口配置失败: %v", err)
	} else if windowConfig.Enabled {
		app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(_ *application.ApplicationEvent) {
			if err := petWindow.Open(); err != nil {
				log.Printf("⚠️ 打开独立宠物窗口失败: %v", err)
			}
		})
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if _, err := runtime.Tick(); err != nil {
					log.Printf("宠物运行时心跳失败: %v", err)
				}
			case <-stopHeartbeat:
				return
			}
		}
	}()

	app.OnShutdown(func() {
		stopOnce.Do(func() {
			close(stopHeartbeat)
			if petKeyboardMonitor != nil {
				if err := petKeyboardMonitor.Stop(); err != nil {
					log.Printf("⚠️ 停止全局键盘监听失败: %v", err)
				}
			}
			petTypingController.Close()
			if petWindow != nil {
				_ = petWindow.Close()
			}
			_ = lifecycle.Close()
		})
	})

	if err := app.Run(); err != nil {
		log.Printf("独立宠物退出失败: %v", err)
		os.Exit(1)
	}
}
