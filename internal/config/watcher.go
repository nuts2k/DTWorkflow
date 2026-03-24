package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// OnChange 注册配置变更回调。
//
// 约定：
// - 只有在新配置加载 + 校验成功并更新 current 后才会触发。
// - 回调串行执行；任一回调 panic 会被 recover，避免影响后续回调与 watcher 运行。
func (m *Manager) OnChange(fn func(oldCfg, newCfg *Config)) {
	if m == nil || fn == nil {
		return
	}
	m.onChangeMu.Lock()
	m.onChange = append(m.onChange, fn)
	m.onChangeMu.Unlock()
}

// WatchConfig 启动配置文件监听。
//
// 说明：
//   - 本方法只实现"配置快照更新 + 回调框架"。
//   - 不负责让 HTTP listener、Redis/SQLite 连接池、Worker Pool 等运行时组件在线重建；
//     这些通常需要重启进程才能完整生效。
//
// 生效边界（最小约定）：
// - 可热更新：纯逻辑类配置（如 notify 路由、review 规则等）在未来可按需由上层订阅回调实现在线生效。
// - 需重启才能完整生效：server/redis/database/worker 等涉及监听端口、连接池、并发模型的配置。
//
// 注意：
// - 仅支持监听"通过 WithConfigFile/SetConfigFile 显式指定的配置文件"。
// - 多次调用是幂等的：只会启动一次 watcher。
func (m *Manager) WatchConfig() error {
	if m == nil {
		return fmt.Errorf("配置管理器不能为空")
	}

	// 检查 Manager 是否已停止
	select {
	case <-m.stopCh:
		return fmt.Errorf("Manager 已停止，无法启动配置监听")
	default:
	}

	// configFile 与底层 viper 实例共用同一把锁保护，避免与 SetConfigFile 并发访问。
	m.viperMu.Lock()
	cfgFile := m.configFile
	m.viperMu.Unlock()

	if filepath.Clean(cfgFile) == "." || cfgFile == "" {
		return fmt.Errorf("未指定配置文件，无法启用热加载")
	}

	m.watchOnce.Do(func() {
		m.watchErr = m.startWatchConfigFile(cfgFile)
	})
	return m.watchErr
}

const debounceDuration = 200 * time.Millisecond

func (m *Manager) startWatchConfigFile(configFile string) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("创建文件 watcher 失败: %w", err)
	}

	cfgPath := filepath.Clean(configFile)
	dir := filepath.Dir(cfgPath)

	// 监听目录而不是单文件：许多编辑器会使用 rename/replace 的方式更新文件。
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return fmt.Errorf("监听配置目录失败: %w", err)
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("配置 watcher goroutine panic", "error", r, "stack", string(debug.Stack()))
			}
		}()
		defer func() {
			_ = w.Close()
		}()

		var (
			debounceTimer   *time.Timer
			debounceTimerCh <-chan time.Time
		)
		stopTimer := func() {
			if debounceTimer == nil {
				debounceTimerCh = nil
				return
			}
			if !debounceTimer.Stop() {
				select {
				case <-debounceTimer.C:
				default:
				}
			}
			debounceTimerCh = nil
		}
		resetTimer := func() {
			if debounceTimer == nil {
				debounceTimer = time.NewTimer(debounceDuration)
			} else {
				stopTimer()
				debounceTimer.Reset(debounceDuration)
			}
			debounceTimerCh = debounceTimer.C
		}

		for {
			select {
			case <-m.stopCh:
				stopTimer()
				return
			case <-debounceTimerCh:
				debounceTimerCh = nil
				select {
				case <-m.stopCh:
					return
				default:
				}
				if err := m.reloadFromDiskWithRetry(cfgPath); err != nil {
					slog.Warn("配置热重载失败", "error", err, "file", cfgPath)
				}
			case evt, ok := <-w.Events:
				if !ok {
					stopTimer()
					return
				}
				if !isConfigRelevantEvent(evt) {
					continue
				}
				// 仅处理目标文件；同时兼容部分编辑器使用 rename/replace 时先移除旧文件、再写入新文件的行为。
				if filepath.Clean(evt.Name) != cfgPath {
					continue
				}
				resetTimer()
			case watchErr, ok := <-w.Errors:
				if !ok {
					stopTimer()
					return
				}
				slog.Warn("配置文件 watcher 错误", "error", watchErr)
			}
		}
	}()

	slog.Info("配置文件 watcher 已启动", "file", cfgPath)
	return nil
}

func (m *Manager) reloadFromDisk() error {
	// 防止并发重载导致重复回调、重复 Unmarshal。
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	// 注意：viper 不是显式并发安全；这里与 Load/SetConfigFile 共用同一把锁串行化访问。
	m.viperMu.Lock()
	if err := m.v.ReadInConfig(); err != nil {
		m.viperMu.Unlock()
		// 读取失败直接返回，保留旧配置。
		return err
	}

	newCfg := &Config{}
	if err := m.v.Unmarshal(newCfg); err != nil {
		m.viperMu.Unlock()
		return err
	}
	m.viperMu.Unlock()

	if err := Validate(newCfg); err != nil {
		return err
	}

	m.mu.Lock()
	oldCfg := m.current
	m.current = newCfg
	m.mu.Unlock()

	m.fireOnChange(oldCfg, newCfg)
	return nil
}

func (m *Manager) reloadFromDiskWithRetry(cfgPath string) error {
	// 许多编辑器会采用"写临时文件 -> rename 覆盖"的策略；在 rename 瞬间，目标文件可能短暂不可读。
	// 这里做极小的退避重试，避免偶发的 ReadInConfig 失败导致错过一次有效更新。
	//
	// 说明：仅对"文件不存在"类错误重试，避免把配置语法错误等问题吞掉并频繁重试。
	if strings.TrimSpace(cfgPath) == "" {
		return m.reloadFromDisk()
	}

	for i := 0; i < 3; i++ {
		err := m.reloadFromDisk()
		if err == nil {
			return nil
		}
		// 仅对"文件不存在"类错误做重试。
		if !isConfigNotFoundErr(err) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return m.reloadFromDisk()
}

func isConfigNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	// viper/fsnotify 在不同平台返回的错误类型不完全一致；这里用最小的字符串判断。
	msg := err.Error()
	return strings.Contains(msg, "no such file") || strings.Contains(msg, "file does not exist")
}

func isConfigRelevantEvent(evt fsnotify.Event) bool {
	return evt.Has(fsnotify.Write) || evt.Has(fsnotify.Create) || evt.Has(fsnotify.Rename)
}

func (m *Manager) fireOnChange(oldCfg, newCfg *Config) {
	m.onChangeMu.RLock()
	cbs := append([]func(oldCfg, newCfg *Config){}, m.onChange...)
	m.onChangeMu.RUnlock()

	oldSnapshot := oldCfg.Clone()
	newSnapshot := newCfg.Clone()

	for _, cb := range cbs {
		if cb == nil {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("配置变更回调 panic", "error", r, "stack", string(debug.Stack()))
				}
			}()
			cb(oldSnapshot, newSnapshot)
		}()
	}
}
