// Package config 加载 config.yaml 与环境变量覆盖（兼容原 fight-the-landlord 结构，
// redis 段替换为 sqlite 段）。
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// 默认配置值
const (
	defaultHost                  = "127.0.0.1"
	defaultPort                  = 3030
	defaultMaxConnections        = 10000
	defaultSQLitePath            = "data/ddz.db"
	defaultTurnTimeout           = 30
	defaultBidTimeout            = 15
	defaultRoomTimeout           = 10
	defaultShutdownTimeout       = 30
	defaultShutdownCheckInterval = 15
	defaultOfflineWaitTimeout    = 30
	defaultRateLimitPerSecond    = 10
	defaultRateLimitPerMinute    = 60
	defaultBanDuration           = 60
	defaultMessageLimitPerSecond = 20
	defaultBotFillTimeout        = 15
)

// Config 服务端配置
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	SQLite   SQLiteConfig   `yaml:"sqlite"`
	Game     GameConfig     `yaml:"game"`
	Security SecurityConfig `yaml:"security"`
	Bot      BotConfig      `yaml:"bot"`
}

// ServerConfig HTTP/WebSocket 服务器配置
type ServerConfig struct {
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	MaxConnections int    `yaml:"max_connections"`
}

// SQLiteConfig SQLite 存储配置
type SQLiteConfig struct {
	Path string `yaml:"path"`
}

// GameConfig 游戏配置
type GameConfig struct {
	TurnTimeout           int `yaml:"turn_timeout"`
	BidTimeout            int `yaml:"bid_timeout"`
	RoomTimeout           int `yaml:"room_timeout"`
	ShutdownTimeout       int `yaml:"shutdown_timeout"`
	ShutdownCheckInterval int `yaml:"shutdown_check_interval"`
	OfflineWaitTimeout    int `yaml:"offline_wait_timeout"`
}

// SecurityConfig 安全配置
type SecurityConfig struct {
	AllowedOrigins []string           `yaml:"allowed_origins"`
	RateLimit      RateLimitConfig    `yaml:"rate_limit"`
	MessageLimit   MessageLimitConfig `yaml:"message_limit"`
}

// RateLimitConfig 连接速率限制配置（per IP）
type RateLimitConfig struct {
	MaxPerSecond int `yaml:"max_per_second"`
	MaxPerMinute int `yaml:"max_per_minute"`
	BanDuration  int `yaml:"ban_duration"`
}

// MessageLimitConfig 消息速率限制配置（per connection）
type MessageLimitConfig struct {
	MaxPerSecond int `yaml:"max_per_second"`
}

// BotConfig 机器人配置（决策引擎为纯代码规则启发式，无外部依赖）
type BotConfig struct {
	Enabled        bool `yaml:"enabled"`
	BotFillTimeout int  `yaml:"bot_fill_timeout"`
}

// --- Duration 便捷方法 ---

func (c *GameConfig) TurnTimeoutDuration() time.Duration {
	return time.Duration(c.TurnTimeout) * time.Second
}

func (c *GameConfig) BidTimeoutDuration() time.Duration {
	return time.Duration(c.BidTimeout) * time.Second
}

func (c *GameConfig) RoomTimeoutDuration() time.Duration {
	return time.Duration(c.RoomTimeout) * time.Minute
}

func (c *GameConfig) ShutdownTimeoutDuration() time.Duration {
	return time.Duration(c.ShutdownTimeout) * time.Minute
}

func (c *GameConfig) ShutdownCheckIntervalDuration() time.Duration {
	return time.Duration(c.ShutdownCheckInterval) * time.Second
}

func (c *GameConfig) OfflineWaitTimeoutDuration() time.Duration {
	return time.Duration(c.OfflineWaitTimeout) * time.Second
}

func (c *RateLimitConfig) BanDurationTime() time.Duration {
	return time.Duration(c.BanDuration) * time.Second
}

// Load 加载配置文件；path 不存在时使用纯默认值
func Load(path string) (*Config, error) {
	cfg := &Config{}

	data, err := os.ReadFile(filepath.Clean(path))
	if err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	setDefaults(cfg)
	loadFromEnv(cfg)
	return cfg, nil
}

// --- 环境变量覆盖 ---

func getEnvStr(key string, target *string) {
	if v := os.Getenv(key); v != "" {
		*target = v
	}
}

func getEnvInt(key string, target *int) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*target = n
		}
	}
}

func getEnvBool(key string, target *bool) {
	if v := os.Getenv(key); v == "true" || v == "1" {
		*target = true
	}
}

func getEnvStrSlice(key string, target *[]string) {
	if v := os.Getenv(key); v != "" {
		*target = strings.Split(v, ",")
	}
}

func loadFromEnv(cfg *Config) {
	getEnvStr("SERVER_HOST", &cfg.Server.Host)
	getEnvInt("SERVER_PORT", &cfg.Server.Port)
	getEnvInt("SERVER_MAX_CONNECTIONS", &cfg.Server.MaxConnections)

	getEnvStr("SQLITE_PATH", &cfg.SQLite.Path)

	getEnvInt("GAME_TURN_TIMEOUT", &cfg.Game.TurnTimeout)
	getEnvInt("GAME_BID_TIMEOUT", &cfg.Game.BidTimeout)
	getEnvInt("GAME_ROOM_TIMEOUT", &cfg.Game.RoomTimeout)
	getEnvInt("GAME_SHUTDOWN_TIMEOUT", &cfg.Game.ShutdownTimeout)
	getEnvInt("GAME_SHUTDOWN_CHECK_INTERVAL", &cfg.Game.ShutdownCheckInterval)
	getEnvInt("GAME_OFFLINE_WAIT_TIMEOUT", &cfg.Game.OfflineWaitTimeout)

	getEnvBool("BOT_ENABLED", &cfg.Bot.Enabled)
	getEnvInt("BOT_FILL_TIMEOUT", &cfg.Bot.BotFillTimeout)

	getEnvStrSlice("SECURITY_ALLOWED_ORIGINS", &cfg.Security.AllowedOrigins)
	getEnvInt("SECURITY_RATE_LIMIT_PER_SECOND", &cfg.Security.RateLimit.MaxPerSecond)
	getEnvInt("SECURITY_MESSAGE_LIMIT_PER_SECOND", &cfg.Security.MessageLimit.MaxPerSecond)
}

// --- 默认值 ---

func setDefaultStr(target *string, defaultVal string) {
	if *target == "" {
		*target = defaultVal
	}
}

func setDefaultInt(target *int, defaultVal int) {
	if *target == 0 {
		*target = defaultVal
	}
}

func setDefaultStrSlice(target *[]string, defaultVal []string) {
	if len(*target) == 0 {
		*target = defaultVal
	}
}

func setDefaults(cfg *Config) {
	setDefaultStr(&cfg.Server.Host, defaultHost)
	setDefaultInt(&cfg.Server.Port, defaultPort)
	setDefaultInt(&cfg.Server.MaxConnections, defaultMaxConnections)

	setDefaultStr(&cfg.SQLite.Path, defaultSQLitePath)

	setDefaultInt(&cfg.Game.TurnTimeout, defaultTurnTimeout)
	setDefaultInt(&cfg.Game.BidTimeout, defaultBidTimeout)
	setDefaultInt(&cfg.Game.RoomTimeout, defaultRoomTimeout)
	setDefaultInt(&cfg.Game.ShutdownTimeout, defaultShutdownTimeout)
	setDefaultInt(&cfg.Game.ShutdownCheckInterval, defaultShutdownCheckInterval)
	setDefaultInt(&cfg.Game.OfflineWaitTimeout, defaultOfflineWaitTimeout)

	setDefaultStrSlice(&cfg.Security.AllowedOrigins, []string{"*"})
	setDefaultInt(&cfg.Security.RateLimit.MaxPerSecond, defaultRateLimitPerSecond)
	setDefaultInt(&cfg.Security.RateLimit.MaxPerMinute, defaultRateLimitPerMinute)
	setDefaultInt(&cfg.Security.RateLimit.BanDuration, defaultBanDuration)
	setDefaultInt(&cfg.Security.MessageLimit.MaxPerSecond, defaultMessageLimitPerSecond)

	setDefaultInt(&cfg.Bot.BotFillTimeout, defaultBotFillTimeout)
}
