package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// EnvConfigPath is the environment variable that points at a config file.
const EnvConfigPath = "HUGINN_CONFIG"

// Locator finds the config file. Fields are injected for tests; use
// SystemLocator in production.
type Locator struct {
	Getenv        func(string) string
	UserConfigDir func() (string, error)
	UserHomeDir   func() (string, error)
	ReadFile      func(string) ([]byte, error)
}

// SystemLocator returns a Locator backed by the operating system.
func SystemLocator() Locator {
	return Locator{Getenv: os.Getenv, UserConfigDir: os.UserConfigDir, UserHomeDir: os.UserHomeDir, ReadFile: os.ReadFile}
}

// Candidates returns the default config paths in lookup order: the OS
// config directory (XDG on Linux, Application Support on macOS, AppData on
// Windows), then ~/.config/huginn/config.yaml.
func (l Locator) Candidates() []string {
	var out []string
	if dir, err := l.UserConfigDir(); err == nil {
		out = append(out, filepath.Join(dir, "huginn", "config.yaml"))
	}
	if home, err := l.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".config", "huginn", "config.yaml")
		if len(out) == 0 || out[0] != p {
			out = append(out, p)
		}
	}
	return out
}

// Load finds, parses, defaults and validates the configuration. explicit is
// the --config flag value (empty if absent). It returns the path actually
// read, or "" when built-in defaults are used because no file exists.
func Load(l Locator, explicit string) (*Config, string, error) {
	path := explicit
	if path == "" {
		path = l.Getenv(EnvConfigPath)
	}
	if path != "" {
		data, err := l.ReadFile(path)
		if err != nil {
			return nil, path, fmt.Errorf("read config: %w", err)
		}
		c, err := Parse(data, path)
		return c, path, err
	}
	for _, p := range l.Candidates() {
		data, err := l.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, p, fmt.Errorf("read config: %w", err)
		}
		c, err := Parse(data, p)
		return c, p, err
	}
	c := Default()
	return c, "", Validate(c, "")
}

// Parse decodes YAML strictly, applies defaults and validates. name is used
// in error messages. All problems are reported at once.
func Parse(data []byte, name string) (*Config, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("%s: %w", displayName(name), err)
	}
	c := &Config{}
	if len(root.Content) > 0 {
		if probs := checkKeys(root.Content[0], typeOfConfig, ""); len(probs) > 0 {
			return nil, &Error{File: name, Problems: probs}
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(c); err != nil {
			return nil, fmt.Errorf("%s: %w", displayName(name), err)
		}
	}
	applyDefaults(c)
	if err := Validate(c, name); err != nil {
		return nil, err
	}
	return c, nil
}

func displayName(name string) string {
	if name == "" {
		return "config"
	}
	return name
}
