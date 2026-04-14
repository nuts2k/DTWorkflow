package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/dtw"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "管理服务器认证",
}

// --- login ---

var (
	loginName  string
	loginURL   string
	loginToken string
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "登录并保存服务器配置",
	RunE: func(cmd *cobra.Command, args []string) error {
		if configPath == "" {
			configPath = dtw.DefaultConfigPath()
		}

		// 加载或创建空配置
		cfg, err := dtw.LoadHostsConfig(configPath)
		if err != nil {
			cfg = &dtw.HostsConfig{Servers: make(map[string]dtw.ServerConfig)}
		}
		if cfg.Servers == nil {
			cfg.Servers = make(map[string]dtw.ServerConfig)
		}

		name, url, token := loginName, loginURL, loginToken

		// 交互式输入缺失字段
		if name == "" || url == "" || token == "" {
			scanner := bufio.NewScanner(os.Stdin)

			if name == "" {
				fmt.Print("服务器名称: ")
				scanner.Scan()
				name = strings.TrimSpace(scanner.Text())
			}
			if url == "" {
				fmt.Print("服务器 URL (如 http://192.168.1.100:8080): ")
				scanner.Scan()
				url = strings.TrimSpace(scanner.Text())
			}
			if token == "" {
				fmt.Print("API Token: ")
				scanner.Scan()
				token = strings.TrimSpace(scanner.Text())
			}
		}

		if name == "" || url == "" || token == "" {
			return fmt.Errorf("名称、URL 和 Token 均为必填")
		}

		// 去除尾部斜杠
		url = strings.TrimRight(url, "/")

		// 连通性验证
		testClient := dtw.NewClient(url, token)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := testClient.Do(ctx, "GET", "/api/v1/status", nil, nil); err != nil {
			return fmt.Errorf("连接服务器失败: %w", err)
		}

		cfg.Servers[name] = dtw.ServerConfig{URL: url, Token: token}
		if cfg.Active == "" {
			cfg.Active = name
		}

		if err := dtw.SaveHostsConfig(configPath, cfg); err != nil {
			return err
		}

		printer.PrintHuman("已保存服务器 %q (%s)", name, url)
		if cfg.Active == name {
			printer.PrintHuman("当前活跃服务器: %s", name)
		}
		return nil
	},
}

// --- logout ---

var logoutCmd = &cobra.Command{
	Use:   "logout [name]",
	Short: "删除已保存的服务器配置",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if configPath == "" {
			configPath = dtw.DefaultConfigPath()
		}

		cfg, err := dtw.LoadHostsConfig(configPath)
		if err != nil {
			return fmt.Errorf("加载配置失败: %w", err)
		}

		name := args[0]
		if _, ok := cfg.Servers[name]; !ok {
			return fmt.Errorf("服务器 %q 不存在", name)
		}

		delete(cfg.Servers, name)
		if cfg.Active == name {
			cfg.Active = ""
			// 如果还有其他服务器，自动切换到第一个
			for k := range cfg.Servers {
				cfg.Active = k
				break
			}
		}

		if err := dtw.SaveHostsConfig(configPath, cfg); err != nil {
			return err
		}
		printer.PrintHuman("已删除服务器 %q", name)
		return nil
	},
}

// --- status ---

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "显示当前服务器配置",
	RunE: func(cmd *cobra.Command, args []string) error {
		if configPath == "" {
			configPath = dtw.DefaultConfigPath()
		}

		cfg, err := dtw.LoadHostsConfig(configPath)
		if err != nil {
			return fmt.Errorf("加载配置失败: %w\n请先运行 dtw auth login", err)
		}

		if flagJSON {
			type serverInfo struct {
				Name   string `json:"name"`
				URL    string `json:"url"`
				Active bool   `json:"active"`
			}
			var list []serverInfo
			for name, srv := range cfg.Servers {
				list = append(list, serverInfo{
					Name:   name,
					URL:    srv.URL,
					Active: name == cfg.Active,
				})
			}
			return printer.PrintJSON(list)
		}

		if len(cfg.Servers) == 0 {
			printer.PrintHuman("尚未配置任何服务器，请运行 dtw auth login")
			return nil
		}

		for name, srv := range cfg.Servers {
			marker := "  "
			if name == cfg.Active {
				marker = "* "
			}
			printer.PrintHuman("%s%s\t%s", marker, name, srv.URL)
		}
		return nil
	},
}

// --- switch ---

var switchCmd = &cobra.Command{
	Use:   "switch [name]",
	Short: "切换活跃服务器",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if configPath == "" {
			configPath = dtw.DefaultConfigPath()
		}

		cfg, err := dtw.LoadHostsConfig(configPath)
		if err != nil {
			return fmt.Errorf("加载配置失败: %w", err)
		}

		name := args[0]
		if _, ok := cfg.Servers[name]; !ok {
			return fmt.Errorf("服务器 %q 不存在", name)
		}

		cfg.Active = name
		if err := dtw.SaveHostsConfig(configPath, cfg); err != nil {
			return err
		}

		printer.PrintHuman("已切换到服务器 %q", name)
		return nil
	},
}

func init() {
	loginCmd.Flags().StringVar(&loginName, "name", "", "服务器名称")
	loginCmd.Flags().StringVar(&loginURL, "url", "", "服务器 URL")
	loginCmd.Flags().StringVar(&loginToken, "token", "", "API Token")

	authCmd.AddCommand(loginCmd)
	authCmd.AddCommand(logoutCmd)
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(switchCmd)
	rootCmd.AddCommand(authCmd)
}
