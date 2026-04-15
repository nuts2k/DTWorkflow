# 仓库级飞书通知覆盖 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 支持仓库级飞书 Webhook 覆盖，不同仓库可发送通知到不同飞书群，未配置时回退全局飞书配置。

**Architecture:** 在 `NotifyOverride` 中新增 `Feishu *FeishuOverride` 字段。`configDrivenNotifier` 构造 per-repo Router 时，根据仓库配置注入专属 `FeishuNotifier` 实例。不修改 `internal/notify/` 包。

**Tech Stack:** Go, Viper (mapstructure), Gin, asynq

**Design Doc:** `docs/superpowers/specs/2026-04-15-repo-feishu-override-design.md`

---

### Task 1: 新增 FeishuOverride 结构体与 NotifyOverride 字段

**Files:**
- Modify: `internal/config/config.go:14-31` (新增 `FeishuOverride` 结构体)
- Modify: `internal/config/repo_config.go:10-13` (给 `NotifyOverride` 加 `Feishu` 字段)

**Step 1: 在 `config.go` 中新增 `FeishuOverride` 结构体**

在 `RouteConfig` 结构体之后（约 131 行后）添加：

```go
// FeishuOverride 仓库级飞书 Webhook 覆盖配置。
type FeishuOverride struct {
	WebhookURL string `mapstructure:"webhook_url"`
	Secret     string `mapstructure:"secret"`
}
```

**Step 2: 在 `repo_config.go` 的 `NotifyOverride` 中添加 `Feishu` 字段**

将 `NotifyOverride` 从：
```go
type NotifyOverride struct {
	Routes []RouteConfig `mapstructure:"routes"`
}
```
改为：
```go
type NotifyOverride struct {
	Routes []RouteConfig  `mapstructure:"routes"`
	Feishu *FeishuOverride `mapstructure:"feishu"`
}
```

**Step 3: 运行编译确认无语法错误**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go build ./...`
Expected: 编译通过

**Step 4: Commit**

```bash
git add internal/config/config.go internal/config/repo_config.go
git commit -m "feat(config): 新增 FeishuOverride 结构体与 NotifyOverride.Feishu 字段"
```

---

### Task 2: 新增 ResolveFeishuOverride 方法（TDD）

**Files:**
- Modify: `internal/config/repo_config.go` (新增方法)
- Modify: `internal/config/repo_config_test.go` (新增测试)

**Step 1: 编写 ResolveFeishuOverride 的测试用例**

在 `repo_config_test.go` 末尾追加：

```go
func TestResolveFeishuOverride(t *testing.T) {
	t.Run("nil config 返回 nil", func(t *testing.T) {
		var c *Config
		if got := c.ResolveFeishuOverride("any/repo"); got != nil {
			t.Errorf("nil config 应返回 nil，得到: %+v", got)
		}
	})

	t.Run("无匹配仓库返回 nil", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:   "other/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{WebhookURL: "https://example.com/hook"}},
		}}
		if got := cfg.ResolveFeishuOverride("acme/repo"); got != nil {
			t.Errorf("不匹配 repo 应返回 nil，得到: %+v", got)
		}
	})

	t.Run("匹配仓库但 Notify 为 nil 返回 nil", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{Name: "acme/repo", Notify: nil}}
		if got := cfg.ResolveFeishuOverride("acme/repo"); got != nil {
			t.Errorf("nil Notify 应返回 nil，得到: %+v", got)
		}
	})

	t.Run("匹配仓库但 Feishu 为 nil 返回 nil", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:   "acme/repo",
			Notify: &NotifyOverride{Routes: []RouteConfig{{Repo: "*"}}},
		}}
		if got := cfg.ResolveFeishuOverride("acme/repo"); got != nil {
			t.Errorf("nil Feishu 应返回 nil，得到: %+v", got)
		}
	})

	t.Run("匹配仓库有 Feishu 覆盖时返回覆盖", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name: "acme/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{
				WebhookURL: "https://example.com/hook/repo",
				Secret:     "repo-secret",
			}},
		}}
		got := cfg.ResolveFeishuOverride("acme/repo")
		if got == nil {
			t.Fatal("有覆盖时不应返回 nil")
		}
		if got.WebhookURL != "https://example.com/hook/repo" {
			t.Errorf("WebhookURL = %q, want %q", got.WebhookURL, "https://example.com/hook/repo")
		}
		if got.Secret != "repo-secret" {
			t.Errorf("Secret = %q, want %q", got.Secret, "repo-secret")
		}
	})

	t.Run("多仓库匹配返回第一个", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{
			{Name: "acme/repo", Notify: &NotifyOverride{Feishu: &FeishuOverride{WebhookURL: "https://first"}}},
			{Name: "acme/repo", Notify: &NotifyOverride{Feishu: &FeishuOverride{WebhookURL: "https://second"}}},
		}
		got := cfg.ResolveFeishuOverride("acme/repo")
		if got == nil || got.WebhookURL != "https://first" {
			t.Errorf("重复 repo 应返回第一个匹配项，得到: %+v", got)
		}
	})
}
```

**Step 2: 运行测试确认失败**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/config/ -run TestResolveFeishuOverride -v`
Expected: 编译失败（`ResolveFeishuOverride` 方法不存在）

**Step 3: 实现 ResolveFeishuOverride 方法**

在 `repo_config.go` 的 `ResolveNotifyRoutes` 方法之后添加：

```go
// ResolveFeishuOverride 返回指定仓库的飞书配置覆盖。
// 返回 nil 表示使用全局配置。
func (c *Config) ResolveFeishuOverride(repoFullName string) *FeishuOverride {
	if c == nil {
		return nil
	}
	for _, repo := range c.Repos {
		if repo.Name == repoFullName && repo.Notify != nil && repo.Notify.Feishu != nil {
			return repo.Notify.Feishu
		}
	}
	return nil
}
```

**Step 4: 运行测试确认通过**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/config/ -run TestResolveFeishuOverride -v`
Expected: 全部 PASS

**Step 5: Commit**

```bash
git add internal/config/repo_config.go internal/config/repo_config_test.go
git commit -m "feat(config): 新增 ResolveFeishuOverride 仓库级飞书覆盖解析"
```

---

### Task 3: Clone 深拷贝补充 FeishuOverride（TDD）

**Files:**
- Modify: `internal/config/config.go:386-407` (`Clone` 方法的 Repos 深拷贝部分)
- Modify: `internal/config/config_test.go` (新增测试)

**Step 1: 编写 Clone FeishuOverride 深拷贝测试**

在 `config_test.go` 末尾追加：

```go
func TestClone_FeishuOverrideDeepCopy(t *testing.T) {
	t.Parallel()

	original := &Config{
		Server:  ServerConfig{Port: 8080},
		Gitea:   GiteaConfig{URL: "http://gitea:3000", Token: "test-token"},
		Claude:  ClaudeConfig{APIKey: "test-api-key"},
		Redis:   RedisConfig{Addr: "localhost:6379"},
		Webhook: WebhookConfig{Secret: "test-secret"},
		Notify: NotifyConfig{
			DefaultChannel: "gitea",
			Channels:       map[string]ChannelConfig{"gitea": {Enabled: true}},
		},
		Repos: []RepoConfig{{
			Name: "acme/repo",
			Notify: &NotifyOverride{
				Feishu: &FeishuOverride{
					WebhookURL: "https://original.com/hook",
					Secret:     "original-secret",
				},
			},
		}},
	}

	cloned := original.Clone()

	// 修改 clone 不应影响原始对象
	cloned.Repos[0].Notify.Feishu.WebhookURL = "https://modified.com/hook"
	cloned.Repos[0].Notify.Feishu.Secret = "modified-secret"

	if original.Repos[0].Notify.Feishu.WebhookURL != "https://original.com/hook" {
		t.Errorf("修改 clone 后原始 WebhookURL 被改变: %q", original.Repos[0].Notify.Feishu.WebhookURL)
	}
	if original.Repos[0].Notify.Feishu.Secret != "original-secret" {
		t.Errorf("修改 clone 后原始 Secret 被改变: %q", original.Repos[0].Notify.Feishu.Secret)
	}
}

func TestClone_FeishuOverrideNil(t *testing.T) {
	t.Parallel()

	original := &Config{
		Server:  ServerConfig{Port: 8080},
		Gitea:   GiteaConfig{URL: "http://gitea:3000", Token: "test-token"},
		Claude:  ClaudeConfig{APIKey: "test-api-key"},
		Redis:   RedisConfig{Addr: "localhost:6379"},
		Webhook: WebhookConfig{Secret: "test-secret"},
		Notify: NotifyConfig{
			DefaultChannel: "gitea",
			Channels:       map[string]ChannelConfig{"gitea": {Enabled: true}},
		},
		Repos: []RepoConfig{{
			Name:   "acme/repo",
			Notify: &NotifyOverride{Routes: []RouteConfig{{Repo: "*"}}},
		}},
	}

	cloned := original.Clone()
	if cloned.Repos[0].Notify.Feishu != nil {
		t.Error("Feishu 为 nil 时 clone 也应为 nil")
	}
}
```

**Step 2: 运行测试确认 FeishuOverrideDeepCopy 失败**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/config/ -run TestClone_FeishuOverride -v`
Expected: `TestClone_FeishuOverrideDeepCopy` FAIL（浅拷贝导致修改 clone 影响了原始对象）

**Step 3: 在 Clone 方法中补充 FeishuOverride 深拷贝**

在 `config.go` 的 `Clone()` 方法中，找到 Repos 深拷贝部分的 `if repo.Notify != nil {` 块内，在 Routes 深拷贝之后、`clone.Repos[i].Notify = &notifyCopy` 之前，添加：

```go
				if repo.Notify.Feishu != nil {
					feishuCopy := *repo.Notify.Feishu
					notifyCopy.Feishu = &feishuCopy
				}
```

**Step 4: 运行测试确认通过**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/config/ -run TestClone_FeishuOverride -v`
Expected: 全部 PASS

**Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): Clone 深拷贝补充 FeishuOverride"
```

---

### Task 4: 配置校验 - 仓库级飞书覆盖（TDD）

**Files:**
- Modify: `internal/config/validate.go:176-186` (在 repos 校验块之后添加)
- Modify: `internal/config/validate_test.go` (新增测试)

**Step 1: 编写仓库级飞书校验的测试用例**

在 `validate_test.go` 末尾追加：

```go
func TestValidate_RepoFeishuOverride(t *testing.T) {
	// 辅助函数：构造启用了全局飞书渠道的基础配置
	feishuBaseConfig := func() *Config {
		cfg := validBaseConfig()
		cfg.Notify.Channels["feishu"] = ChannelConfig{
			Enabled: true,
			Options: map[string]string{
				"webhook_url": "https://open.feishu.cn/open-apis/bot/v2/hook/global",
			},
		}
		return cfg
	}

	t.Run("合法仓库级飞书覆盖通过", func(t *testing.T) {
		cfg := feishuBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name: "acme/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{
				WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/repo",
				Secret:     "repo-secret",
			}},
		}}
		if err := Validate(cfg); err != nil {
			t.Fatalf("合法仓库级飞书覆盖应通过: %v", err)
		}
	})

	t.Run("webhook_url 为空报错", func(t *testing.T) {
		cfg := feishuBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:   "acme/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{WebhookURL: ""}},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("空 webhook_url 应报错")
		}
		if !strings.Contains(err.Error(), "webhook_url") {
			t.Errorf("错误应包含 webhook_url: %v", err)
		}
	})

	t.Run("webhook_url 仅空白报错", func(t *testing.T) {
		cfg := feishuBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:   "acme/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{WebhookURL: "   "}},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("仅空白 webhook_url 应报错")
		}
	})

	t.Run("webhook_url 格式无效报错", func(t *testing.T) {
		cfg := feishuBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:   "acme/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{WebhookURL: "not-a-url"}},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("无效 webhook_url 格式应报错")
		}
		if !strings.Contains(err.Error(), "格式无效") {
			t.Errorf("错误应包含 '格式无效': %v", err)
		}
	})

	t.Run("全局飞书渠道未启用时报错", func(t *testing.T) {
		cfg := validBaseConfig() // 全局无飞书渠道
		cfg.Repos = []RepoConfig{{
			Name: "acme/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{
				WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/repo",
			}},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("全局飞书未启用时仓库级覆盖应报错")
		}
		if !strings.Contains(err.Error(), "全局飞书渠道未启用") {
			t.Errorf("错误应包含 '全局飞书渠道未启用': %v", err)
		}
	})

	t.Run("全局飞书 disabled 时报错", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Notify.Channels["feishu"] = ChannelConfig{Enabled: false}
		cfg.Repos = []RepoConfig{{
			Name: "acme/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{
				WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/repo",
			}},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("全局飞书 disabled 时仓库级覆盖应报错")
		}
	})

	t.Run("无 secret 合法（不强制）", func(t *testing.T) {
		cfg := feishuBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name: "acme/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{
				WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/repo",
				Secret:     "",
			}},
		}}
		if err := Validate(cfg); err != nil {
			t.Fatalf("无 secret 应合法: %v", err)
		}
	})

	t.Run("Feishu 为 nil 不校验", func(t *testing.T) {
		cfg := feishuBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:   "acme/repo",
			Notify: &NotifyOverride{Routes: []RouteConfig{{Repo: "*", Channels: []string{"gitea"}}}},
		}}
		if err := Validate(cfg); err != nil {
			t.Fatalf("Feishu nil 不应校验: %v", err)
		}
	})

	t.Run("Notify 为 nil 不校验", func(t *testing.T) {
		cfg := feishuBaseConfig()
		cfg.Repos = []RepoConfig{{Name: "acme/repo", Notify: nil}}
		if err := Validate(cfg); err != nil {
			t.Fatalf("Notify nil 不应校验: %v", err)
		}
	})
}
```

**Step 2: 运行测试确认失败（部分测试应 fail 因缺少校验逻辑）**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/config/ -run TestValidate_RepoFeishuOverride -v`
Expected: `webhook_url 为空报错`、`格式无效`、`全局飞书渠道未启用` 等测试 FAIL

**Step 3: 在 validate.go 中添加仓库级飞书校验**

在 `validate.go` 的 `// repos[].notify.routes 引用的渠道...` 循环之后（约 176 行后），添加：

```go
	// 仓库级飞书覆盖校验
	for _, repo := range cfg.Repos {
		if repo.Notify == nil || repo.Notify.Feishu == nil {
			continue
		}
		f := repo.Notify.Feishu

		// webhook_url 必填
		if strings.TrimSpace(f.WebhookURL) == "" {
			errs = append(errs, fmt.Errorf(
				"repos[%s].notify.feishu: webhook_url 不能为空", repo.Name))
			continue
		}

		// webhook_url 格式校验
		if u, err := url.Parse(f.WebhookURL); err != nil ||
			(u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf(
				"repos[%s].notify.feishu.webhook_url 格式无效", repo.Name))
		}

		// 全局飞书渠道必须已启用
		if feishuCfg, ok := cfg.Notify.Channels["feishu"]; !ok || !feishuCfg.Enabled {
			errs = append(errs, fmt.Errorf(
				"repos[%s].notify.feishu: 全局飞书渠道未启用，仓库级覆盖无效", repo.Name))
		}
	}
```

**Step 4: 运行测试确认通过**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/config/ -run TestValidate_RepoFeishuOverride -v`
Expected: 全部 PASS

**Step 5: 运行全部 config 测试确认无回归**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/config/ -v`
Expected: 全部 PASS

**Step 6: Commit**

```bash
git add internal/config/validate.go internal/config/validate_test.go
git commit -m "feat(config): 仓库级飞书覆盖配置校验（webhook_url必填/格式/全局启用前提）"
```

---

### Task 5: hasRepoNotifyOverride 扩展 Feishu 检查（TDD）

**Files:**
- Modify: `internal/cmd/serve_notify.go:106-116` (`hasRepoNotifyOverride` 方法)
- Modify: `internal/cmd/serve_notify_test.go` (新增测试)

**Step 1: 编写 hasRepoNotifyOverride 扩展测试**

在 `serve_notify_test.go` 末尾追加：

```go
func TestHasRepoNotifyOverride_FeishuOnly(t *testing.T) {
	cfg := &config.Config{
		Repos: []config.RepoConfig{{
			Name: "acme/repo",
			Notify: &config.NotifyOverride{
				Feishu: &config.FeishuOverride{WebhookURL: "https://example.com/hook"},
			},
		}},
	}
	n := &configDrivenNotifier{cfg: cfg}

	if !n.hasRepoNotifyOverride("acme/repo") {
		t.Error("仅配置 Feishu 覆盖时应返回 true")
	}
}

func TestHasRepoNotifyOverride_BothRoutesAndFeishu(t *testing.T) {
	cfg := &config.Config{
		Repos: []config.RepoConfig{{
			Name: "acme/repo",
			Notify: &config.NotifyOverride{
				Routes: []config.RouteConfig{{Repo: "*"}},
				Feishu: &config.FeishuOverride{WebhookURL: "https://example.com/hook"},
			},
		}},
	}
	n := &configDrivenNotifier{cfg: cfg}

	if !n.hasRepoNotifyOverride("acme/repo") {
		t.Error("Routes 和 Feishu 都配置时应返回 true")
	}
}

func TestHasRepoNotifyOverride_NeitherRoutesNorFeishu(t *testing.T) {
	cfg := &config.Config{
		Repos: []config.RepoConfig{{
			Name:   "acme/repo",
			Notify: &config.NotifyOverride{},
		}},
	}
	n := &configDrivenNotifier{cfg: cfg}

	if n.hasRepoNotifyOverride("acme/repo") {
		t.Error("Routes 和 Feishu 都为 nil 时应返回 false")
	}
}
```

**Step 2: 运行测试确认 FeishuOnly 失败**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/cmd/ -run TestHasRepoNotifyOverride -v`
Expected: `TestHasRepoNotifyOverride_FeishuOnly` FAIL

**Step 3: 更新 hasRepoNotifyOverride**

将 `serve_notify.go:111` 的条件从：
```go
if repo.Name == repoFullName && repo.Notify != nil && repo.Notify.Routes != nil {
```
改为：
```go
if repo.Name == repoFullName && repo.Notify != nil &&
	(repo.Notify.Routes != nil || repo.Notify.Feishu != nil) {
```

**Step 4: 运行测试确认通过**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/cmd/ -run TestHasRepoNotifyOverride -v`
Expected: 全部 PASS

**Step 5: Commit**

```bash
git add internal/cmd/serve_notify.go internal/cmd/serve_notify_test.go
git commit -m "feat(notify): hasRepoNotifyOverride 支持飞书覆盖检测"
```

---

### Task 6: newRouter 注入仓库级 FeishuNotifier（TDD）

**Files:**
- Modify: `internal/cmd/serve_notify.go:118-139` (`newRouter` 方法)
- Modify: `internal/cmd/serve_notify_test.go` (新增测试)

**Step 1: 编写 newRouter 仓库级飞书注入测试**

在 `serve_notify_test.go` 末尾追加：

```go
func TestConfigDrivenNotifier_RepoFeishuOverride_UsesRepoRouter(t *testing.T) {
	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "feishu",
			Channels: map[string]config.ChannelConfig{
				"feishu": {Enabled: true},
			},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"feishu"}}},
		},
		Repos: []config.RepoConfig{{
			Name: "acme/repo",
			Notify: &config.NotifyOverride{
				Feishu: &config.FeishuOverride{
					WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/repo-specific",
				},
			},
		}},
	}

	n := &configDrivenNotifier{
		cfg:            cfg,
		feishuNotifier: noopNotifier{name: "feishu"},
		logger:         slog.Default(),
	}

	// 有仓库级飞书覆盖的仓库应该走 per-repo Router
	r1, err := n.getRouter("acme/repo")
	if err != nil {
		t.Fatalf("getRouter(acme/repo) error: %v", err)
	}

	// 无覆盖的仓库应该走全局 Router
	r2, err := n.getRouter("other/repo")
	if err != nil {
		t.Fatalf("getRouter(other/repo) error: %v", err)
	}

	// 两者应该是不同的 Router 实例
	if r1 == r2 {
		t.Error("有飞书覆盖的仓库应使用独立 Router，但与全局 Router 相同")
	}

	// per-repo Router 应被缓存
	if len(n.routers) != 1 {
		t.Errorf("override routers 数量 = %d, want 1", len(n.routers))
	}
}

func TestConfigDrivenNotifier_RepoFeishuOverride_NoGlobalFeishu(t *testing.T) {
	// 仅有仓库级飞书覆盖，全局无飞书 notifier
	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "gitea",
			Channels: map[string]config.ChannelConfig{
				"gitea":  {Enabled: true},
				"feishu": {Enabled: true},
			},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"gitea"}}},
		},
		Repos: []config.RepoConfig{{
			Name: "acme/repo",
			Notify: &config.NotifyOverride{
				Feishu: &config.FeishuOverride{
					WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/repo",
				},
			},
		}},
	}

	n := &configDrivenNotifier{
		cfg:           cfg,
		giteaNotifier: noopNotifier{name: "gitea"},
		logger:        slog.Default(),
	}

	router, err := n.getRouter("acme/repo")
	if err != nil {
		t.Fatalf("getRouter error: %v", err)
	}
	if router == nil {
		t.Fatal("router 不应为 nil")
	}
}

func TestConfigDrivenNotifier_RepoFeishuOverride_WithSecret(t *testing.T) {
	cfg := &config.Config{
		Notify: config.NotifyConfig{
			DefaultChannel: "feishu",
			Channels: map[string]config.ChannelConfig{
				"feishu": {Enabled: true},
			},
			Routes: []config.RouteConfig{{Repo: "*", Events: []string{"*"}, Channels: []string{"feishu"}}},
		},
		Repos: []config.RepoConfig{{
			Name: "acme/repo",
			Notify: &config.NotifyOverride{
				Feishu: &config.FeishuOverride{
					WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/repo",
					Secret:     "repo-secret",
				},
			},
		}},
	}

	n := &configDrivenNotifier{
		cfg:            cfg,
		feishuNotifier: noopNotifier{name: "feishu"},
		logger:         slog.Default(),
	}

	// 有 secret 的仓库级覆盖也应正常构造
	router, err := n.getRouter("acme/repo")
	if err != nil {
		t.Fatalf("getRouter error: %v", err)
	}
	if router == nil {
		t.Fatal("router 不应为 nil")
	}
}
```

**Step 2: 运行测试确认部分测试失败**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/cmd/ -run TestConfigDrivenNotifier_RepoFeishuOverride -v`
Expected: `UsesRepoRouter` FAIL（当前 newRouter 不构造仓库级 feishu notifier）

**Step 3: 更新 newRouter 注入仓库级 FeishuNotifier**

将 `serve_notify.go` 中的 `newRouter` 方法从：
```go
func (n *configDrivenNotifier) newRouter(repoFullName string) (*notify.Router, error) {
	rules, fallback := buildNotifyRules(n.cfg, repoFullName)

	opts := []notify.RouterOption{
		notify.WithRules(rules),
		notify.WithFallback(fallback),
		notify.WithRouterLogger(n.logger),
	}

	if n.giteaNotifier != nil {
		opts = append(opts, notify.WithNotifier(n.giteaNotifier))
	}
	if n.feishuNotifier != nil {
		opts = append(opts, notify.WithNotifier(n.feishuNotifier))
	}

	router, err := notify.NewRouter(opts...)
	if err != nil {
		return nil, fmt.Errorf("构造通知路由失败: %w", err)
	}
	return router, nil
}
```

改为：
```go
func (n *configDrivenNotifier) newRouter(repoFullName string) (*notify.Router, error) {
	rules, fallback := buildNotifyRules(n.cfg, repoFullName)

	opts := []notify.RouterOption{
		notify.WithRules(rules),
		notify.WithFallback(fallback),
		notify.WithRouterLogger(n.logger),
	}

	if n.giteaNotifier != nil {
		opts = append(opts, notify.WithNotifier(n.giteaNotifier))
	}

	// 仓库级飞书覆盖：构造专属 FeishuNotifier
	if override := n.cfg.ResolveFeishuOverride(repoFullName); override != nil {
		var feishuOpts []notify.FeishuOption
		if override.Secret != "" {
			feishuOpts = append(feishuOpts, notify.WithFeishuSecret(override.Secret))
		}
		feishuOpts = append(feishuOpts, notify.WithFeishuLogger(n.logger))
		fn, err := notify.NewFeishuNotifier(override.WebhookURL, feishuOpts...)
		if err != nil {
			return nil, fmt.Errorf("构造仓库 %s 飞书通知器失败: %w", repoFullName, err)
		}
		opts = append(opts, notify.WithNotifier(fn))
	} else if n.feishuNotifier != nil {
		// 无覆盖，使用全局
		opts = append(opts, notify.WithNotifier(n.feishuNotifier))
	}

	router, err := notify.NewRouter(opts...)
	if err != nil {
		return nil, fmt.Errorf("构造通知路由失败: %w", err)
	}
	return router, nil
}
```

**Step 4: 运行测试确认通过**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/cmd/ -run TestConfigDrivenNotifier_RepoFeishuOverride -v`
Expected: 全部 PASS

**Step 5: 运行全部 serve_notify 测试确认无回归**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/cmd/ -v`
Expected: 全部 PASS

**Step 6: Commit**

```bash
git add internal/cmd/serve_notify.go internal/cmd/serve_notify_test.go
git commit -m "feat(notify): newRouter 注入仓库级 FeishuNotifier"
```

---

### Task 7: 更新示例配置

**Files:**
- Modify: `configs/dtworkflow.example.yaml:126-155` (repos 示例部分)

**Step 1: 在示例配置的 repos 部分添加飞书覆盖示例**

在 `configs/dtworkflow.example.yaml` 的 `repos:` 部分的 `acme/demo` 条目中，`notify:` 下添加飞书覆盖注释示例。在现有 `routes:` 注释示例之后添加：

```yaml
      # 仓库级飞书 Webhook 覆盖（可选）
      # 配置后该仓库的飞书通知将发送到独立的飞书群，未配置时使用全局飞书配置。
      # 前提：全局 notify.channels.feishu 已启用。
      # feishu:
      #   webhook_url: "https://open.feishu.cn/open-apis/bot/v2/hook/repo-xxx"
      #   secret: "repo-secret"  # 可选，无 secret 时不做签名校验
```

**Step 2: 运行示例配置加载测试**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./internal/config/ -run TestExampleConfig_Loadable -v`
Expected: PASS

**Step 3: Commit**

```bash
git add configs/dtworkflow.example.yaml
git commit -m "docs: 示例配置补充仓库级飞书覆盖注释"
```

---

### Task 8: 全量测试与编译验证

**Step 1: 运行全部测试**

Run: `cd /Users/kelin/Workspace/DTWorkflow && go test ./... -count=1`
Expected: 全部 PASS

**Step 2: 交叉编译验证**

Run: `cd /Users/kelin/Workspace/DTWorkflow && GOOS=linux GOARCH=amd64 go build ./cmd/dtworkflow/ && GOOS=linux GOARCH=amd64 go build ./cmd/dtw/`
Expected: 编译成功

**Step 3: 清理构建产物**

Run: `rm -f dtworkflow dtw`
