// 配置加载与校验
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Database struct {
		DSN string `yaml:"dsn"`
	} `yaml:"database"`
	Query struct {
		TimeoutSeconds int `yaml:"timeout_seconds"`
		MaxRows        int `yaml:"max_rows"`
	} `yaml:"query"`
	Security struct {
		AllowedStatements []string `yaml:"allowed_statements"`
		// AllowedTables 表白名单：非空时，list_tables 只展示这些表，
		// run_query/explain_query 中引用其他表会被拒绝；留空表示不限制
		AllowedTables []string `yaml:"allowed_tables"`
	} `yaml:"security"`
}

// Default 返回内置默认值（配置文件里没写的字段用它兜底）
func Default() *Config {
	c := &Config{}
	c.Query.TimeoutSeconds = 10
	c.Query.MaxRows = 500
	c.Security.AllowedStatements = []string{"SELECT", "SHOW", "EXPLAIN", "DESCRIBE", "DESC", "WITH"}
	return c
}

// Load 从 path 加载配置；path 为空时找可执行文件同目录的 config.yaml
func Load(path string) (*Config, error) {
	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(filepath.Dir(exe), "config.yaml")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败（%s）：%w；请复制 config.example.yaml 为 config.yaml 并填写数据库连接信息", path, err)
	}
	cfg := Default()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("配置文件格式错误：%w", err)
	}
	if cfg.Database.DSN == "" {
		return nil, fmt.Errorf("配置缺少 database.dsn，请编辑 %s", path)
	}
	if cfg.Query.TimeoutSeconds <= 0 {
		cfg.Query.TimeoutSeconds = 10
	}
	if cfg.Query.MaxRows <= 0 {
		cfg.Query.MaxRows = 500
	}
	return cfg, nil
}
