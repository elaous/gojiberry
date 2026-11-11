package experimental

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/elaous/gojiberry/internal/transpile"
)

type sqlcConfig struct {
	SQL []struct {
		Gen struct {
			Go struct {
				Package string `yaml:"package" json:"package"`
				Out     string `yaml:"out" json:"out"`
			} `yaml:"go" json:"go"`
		} `yaml:"gen" json:"gen"`
	} `yaml:"sql" json:"sql"`
}

func LoadSQL(in transpile.LoaderInput) (transpile.LoaderResult, error) {
	abs, err := transpile.ResolveResourcePath(in)
	if err != nil {
		return transpile.LoaderResult{}, err
	}

	if _, lookErr := exec.LookPath("sqlc"); lookErr != nil {
		return transpile.LoaderResult{}, fmt.Errorf("xango: .sql import %q requires the `sqlc` CLI on PATH; install it from https://docs.sqlc.dev/en/latest/overview/install.html", in.Path)
	}

	configPath, err := findSqlcConfig(filepath.Dir(abs))
	if err != nil {
		return transpile.LoaderResult{}, fmt.Errorf("xango: .sql import %q: %w", in.Path, err)
	}
	cfg, err := parseSqlcConfig(configPath)
	if err != nil {
		return transpile.LoaderResult{}, fmt.Errorf("xango: .sql import %q: reading %s: %w", in.Path, configPath, err)
	}
	if len(cfg.SQL) == 0 {
		return transpile.LoaderResult{}, fmt.Errorf("xango: .sql import %q: %s has no `sql:` entries (only sqlc's v2 config format is supported)", in.Path, configPath)
	}
	goCfg := cfg.SQL[0].Gen.Go
	if goCfg.Package == "" || goCfg.Out == "" {
		return transpile.LoaderResult{}, fmt.Errorf("xango: .sql import %q: %s's first sql entry has no gen.go.package/gen.go.out", in.Path, configPath)
	}

	cmd := exec.Command("sqlc", "generate", "-f", configPath)
	cmd.Dir = filepath.Dir(configPath)
	if out, genErr := cmd.CombinedOutput(); genErr != nil {
		return transpile.LoaderResult{}, fmt.Errorf("xango: sqlc generate failed for %q: %w\n%s", in.Path, genErr, out)
	}

	moduleRoot, modulePath, err := transpile.FindModuleRoot(in.BaseDir)
	if err != nil {
		return transpile.LoaderResult{}, fmt.Errorf("xango: .sql import %q: %w", in.Path, err)
	}
	outAbs := filepath.Join(filepath.Dir(configPath), goCfg.Out)
	rel, err := filepath.Rel(moduleRoot, outAbs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return transpile.LoaderResult{}, fmt.Errorf("xango: .sql import %q: sqlc output directory %s is outside module root %s", in.Path, outAbs, moduleRoot)
	}

	return transpile.LoaderResult{ImportPath: modulePath + "/" + filepath.ToSlash(rel)}, nil
}

func findSqlcConfig(startDir string) (string, error) {
	dir := startDir
	for {
		for _, name := range []string{"sqlc.yaml", "sqlc.yml", "sqlc.json"} {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no sqlc.yaml/sqlc.yml/sqlc.json found above %s; sqlc-backed .sql imports require a real sqlc project config (see https://docs.sqlc.dev/en/latest/reference/config.html)", startDir)
		}
		dir = parent
	}
}

func parseSqlcConfig(path string) (sqlcConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sqlcConfig{}, err
	}
	var cfg sqlcConfig
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		err = json.Unmarshal(data, &cfg)
	} else {
		err = yaml.Unmarshal(data, &cfg)
	}
	return cfg, err
}
