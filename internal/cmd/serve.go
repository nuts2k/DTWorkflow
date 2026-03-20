package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/cobra"
)

var (
	serveHost string
	servePort int
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "启动 HTTP 服务（API + Webhook 接收器）",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().StringVar(&serveHost, "host", "0.0.0.0", "监听地址")
	serveCmd.Flags().IntVar(&servePort, "port", 8080, "监听端口")
	rootCmd.AddCommand(serveCmd)
}

// runServe 启动 HTTP 服务，注册路由，并支持优雅关闭
func runServe(cmd *cobra.Command, args []string) error {
	// 使用 Release 模式，避免 gin 输出调试信息
	gin.SetMode(gin.ReleaseMode)

	// 使用 gin.New() 而非 gin.Default()，手动控制中间件
	router := gin.New()

	// 注册健康检查路由
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"version": version,
		})
	})

	addr := fmt.Sprintf("%s:%d", serveHost, servePort)

	// 先验证端口可用性，避免 goroutine 中启动失败后主 goroutine 挂起
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听地址 %s 失败: %w", addr, err)
	}

	server := &http.Server{
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 在 goroutine 中启动服务，避免阻塞主 goroutine
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP 服务异常退出", "error", err)
		}
	}()

	// 等待系统信号以触发优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	slog.Info("服务启动", "host", serveHost, "port", servePort)

	<-quit

	signal.Stop(quit)

	// 收到信号后，给予最多 5 秒完成在途请求
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("服务关闭失败", "error", err)
		return err
	}

	slog.Info("服务已停止")
	return nil
}
