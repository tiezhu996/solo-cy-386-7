// Package main 负责加载配置、装配依赖、启动服务。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marketpal/marketpal/internal/config"
	"github.com/marketpal/marketpal/internal/constants"
	"github.com/marketpal/marketpal/internal/database"
	"github.com/marketpal/marketpal/internal/handler"
	"github.com/marketpal/marketpal/internal/repository"
	"github.com/marketpal/marketpal/internal/router"
	"github.com/marketpal/marketpal/internal/service"
	"github.com/marketpal/marketpal/internal/util"
)

func main() {
	cfg := config.Load()
	logger := util.NewLogger(cfg.Env)
	logger.Info(constants.LogConfigLoaded, "db_host", cfg.DBHost, "db_port", cfg.DBPort, "db_name", cfg.DBName)

	db, err := database.Connect(cfg, logger)
	if err != nil {
		logger.Error("connect database failed", "err", err)
		os.Exit(1)
	}

	rdb, err := database.ConnectRedis(cfg, logger)
	if err != nil {
		logger.Warn("connect redis failed, fallback to in-process push", "err", err)
		rdb = nil
	}

	// 仓储层装配。
	userRepo := repository.NewUserRepository(db)
	productRepo := repository.NewProductRepository(db)
	favoriteRepo := repository.NewFavoriteRepository(db)
	addressRepo := repository.NewAddressRepository(db)
	cartRepo := repository.NewCartRepository(db)
	orderRepo := repository.NewOrderRepository(db)
	messageRepo := repository.NewMessageRepository(db)
	reviewRepo := repository.NewReviewRepository(db)
	auditRepo := repository.NewAuditRepository(db)
	exchangeRepo := repository.NewExchangeRepository(db)

	// 服务层装配（构造器注入）。
	userService := service.NewUserService(userRepo, logger, cfg.JWTSecret, cfg.JWTExpireDuration())
	productService := service.NewProductService(productRepo, favoriteRepo, logger)
	addressService := service.NewAddressService(addressRepo, logger)
	cartService := service.NewCartService(cartRepo, productRepo, logger)
	orderService := service.NewOrderService(db, orderRepo, productRepo, addressRepo, cartRepo, logger)
	hub := service.NewHub(rdb, logger)
	messageService := service.NewMessageService(messageRepo, hub, logger)
	reviewService := service.NewReviewService(db, reviewRepo, orderRepo, userService, logger)
	auditService := service.NewAuditService(auditRepo, logger)
	exchangeService := service.NewExchangeService(db, exchangeRepo, productRepo, logger)

	// 处理器装配。
	userHandler := handler.NewUserHandler(userService)
	productHandler := handler.NewProductHandler(productService)
	addressHandler := handler.NewAddressHandler(addressService)
	cartHandler := handler.NewCartHandler(cartService)
	orderHandler := handler.NewOrderHandler(orderService)
	messageHandler := handler.NewMessageHandler(messageService)
	reviewHandler := handler.NewReviewHandler(reviewService)
	auditHandler := handler.NewAuditHandler(auditService)
	wsHandler := handler.NewWSHandler(hub, logger)
	uploadHandler := handler.NewUploadHandler(cfg.UploadDir, cfg.PublicURL)
	exchangeHandler := handler.NewExchangeHandler(exchangeService)

	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New()
	router.Register(engine, cfg, logger,
		userHandler, productHandler, addressHandler, cartHandler, orderHandler,
		messageHandler, reviewHandler, auditHandler, wsHandler, uploadHandler, exchangeHandler, auditService)

	// 换物提案超时扫描：每分钟将到期未处理的生效提案置为 expired 并释放物品。
	exchangeSweepCtx, exchangeSweepCancel := context.WithCancel(context.Background())
	go runExchangeSweeper(exchangeSweepCtx, exchangeService, logger)

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           engine,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info(constants.LogServerStarted, "addr", ":"+cfg.Port, "env", cfg.Env)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server listen failed", "err", err)
			os.Exit(1)
		}
	}()

	// 优雅退出。
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("shutting down server...")
	exchangeSweepCancel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error(constants.LogServerShutdown, "err", err)
	}
}

// runExchangeSweeper 周期执行换物提案超时失效。
func runExchangeSweeper(ctx context.Context, svc *service.ExchangeService, logger *slog.Logger) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := svc.ExpireDue()
			if err != nil {
				logger.Error("exchange proposal expire sweep failed", "err", err)
				continue
			}
			if n > 0 {
				logger.Info("exchange proposals expired by sweeper", "count", n)
			}
		}
	}
}
