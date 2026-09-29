// Package config відповідає за читання налаштувань з config.yaml
// та змінних оточення (env).
//
// Ідея проста: у YAML-файлі лежать "дефолтні" налаштування, а секрети
// і те, що відрізняється між середовищами (локально / Docker), можна
// перевизначити через змінні оточення.
package config

import (
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/goccy/go-yaml"
)

// Config — це вся конфігурація програми.
//
// Текст у зворотних лапках `yaml:"server"` — це "struct tag".
// Він каже YAML-бібліотеці: "поле Server береться з ключа server у файлі".
type Config struct {
	Server       ServerConfig          `yaml:"server"`
	Database     DatabaseConfig        `yaml:"database"`
	Orchestrator OrchestratorConfig    `yaml:"orchestrator"`
	Roles        map[string]RoleConfig `yaml:"roles"` // ключ: architect / developer / reviewer / tester / jira
}

type ServerConfig struct {
	Port int `yaml:"port"`
}

type DatabaseConfig struct {
	// URL — повний рядок підключення. Якщо заданий, решта полів ігнорується.
	// Зазвичай приходить зі змінної оточення DATABASE_URL.
	URL      string `yaml:"url"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbname"`
	SSLMode  string `yaml:"sslmode"`
}

type OrchestratorConfig struct {
	// Папка, де лежать git-репозиторії (наприклад /workspaces/my-repo).
	ReposBaseDir string `yaml:"repos_base_dir"`
	// Папка, куди створюються git worktree для кожної задачі.
	WorktreesDir string `yaml:"worktrees_dir"`
	// Основний репозиторій: у його worktree запускається claude.
	// Можна перевизначити для задачі: --repo або мітка repo:<name> у Jira.
	MainRepo string `yaml:"main_repo"`
	// Репозиторії з repos_base_dir, для яких НЕ створюється worktree.
	// Для всіх інших worktree створюється — задача може зачіпати кілька репозиторіїв.
	ExcludeRepos []string `yaml:"exclude_repos"`
	// Скільки разів Reviewer може повернути задачу Developer'у.
	MaxReviewCycles int `yaml:"max_review_cycles"`
	// Максимальний час роботи одного запуску агента (наприклад "30m").
	AgentTimeout time.Duration `yaml:"agent_timeout"`
	// Чи видаляти worktree після завершення задачі.
	// Гілка з комітами при цьому залишається в репозиторії.
	CleanupWorktree bool `yaml:"cleanup_worktree"`
}

type RoleConfig struct {
	CLIFlags          []string `yaml:"cli_flags"`
	SystemInstruction string   `yaml:"system_instruction"`
	// Необов'язково: модель для цієї ролі (наприклад "opus" або "sonnet").
	Model string `yaml:"model"`
}

// Load читає YAML-файл за шляхом path і повертає готовий Config.
//
// У Go функції часто повертають два значення: результат і помилку.
// Якщо помилки немає — err == nil.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		// %w "загортає" оригінальну помилку, щоб не загубити причину.
		return nil, fmt.Errorf("не вдалося прочитати %s: %w", path, err)
	}

	cfg := defaults()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("помилка у форматі %s: %w", path, err)
	}

	applyEnv(cfg)

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// defaults — значення за замовчуванням, якщо чогось немає у файлі.
func defaults() *Config {
	return &Config{
		Server: ServerConfig{Port: 8080},
		Orchestrator: OrchestratorConfig{
			ReposBaseDir:    "/workspaces",
			WorktreesDir:    "/tmp/workspaces",
			MaxReviewCycles: 3,
			AgentTimeout:    30 * time.Minute,
			CleanupWorktree: true,
		},
	}
}

// applyEnv перевизначає налаштування змінними оточення, якщо вони задані.
func applyEnv(cfg *Config) {
	// Невелика допоміжна функція, оголошена прямо всередині іншої функції.
	override := func(target *string, envName string) {
		if v := os.Getenv(envName); v != "" {
			*target = v
		}
	}

	override(&cfg.Database.URL, "DATABASE_URL")
	override(&cfg.Orchestrator.ReposBaseDir, "REPOS_BASE_DIR")
	override(&cfg.Orchestrator.WorktreesDir, "WORKTREES_DIR")
}

func (c *Config) validate() error {
	for _, role := range []string{"architect", "developer", "reviewer", "tester", "jira"} {
		if _, ok := c.Roles[role]; !ok {
			return fmt.Errorf("у config.yaml не описана роль %q", role)
		}
	}
	if slices.Contains(c.Orchestrator.ExcludeRepos, c.Orchestrator.MainRepo) {
		return fmt.Errorf("main_repo %q не може бути в exclude_repos", c.Orchestrator.MainRepo)
	}
	if c.Orchestrator.MaxReviewCycles < 1 {
		return fmt.Errorf("max_review_cycles має бути >= 1")
	}
	return nil
}

// DatabaseURL повертає рядок підключення до PostgreSQL.
//
// (c *Config) перед назвою означає, що це "метод" типу Config —
// його викликають як cfg.DatabaseURL().
func (c *Config) DatabaseURL() string {
	if c.Database.URL != "" {
		return c.Database.URL
	}
	d := c.Database
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		d.User, d.Password, d.Host, d.Port, d.DBName, d.SSLMode)
}
