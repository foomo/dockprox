package config

import (
	"io"
	"os"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"
	"github.com/pkg/errors"
)

// Defaults returns a Config preloaded with default values.
func Defaults() *Config {
	return &Config{
		Listen:   "127.0.0.1:8888",
		LogLevel: "info",
	}
}

// Load reads YAML from r, merges it over Defaults, and returns the
// validated Config.
func Load(r io.Reader) (*Config, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, errors.Wrap(err, "read config")
	}

	return LoadBytes(buf)
}

// LoadBytes parses YAML from buf, merges it over Defaults, and returns the
// validated Config.
func LoadBytes(buf []byte) (*Config, error) {
	k := koanf.New(".")
	if err := k.Load(rawbytes.Provider(buf), yaml.Parser()); err != nil {
		return nil, errors.Wrap(err, "parse yaml")
	}

	return finalize(k)
}

// LoadFile reads path as YAML, merges it over Defaults, and returns the
// validated Config.
func LoadFile(path string) (*Config, error) {
	k := koanf.New(".")
	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return nil, errors.Wrap(err, "load file")
	}

	return finalize(k)
}

// LoadStdin reads YAML from os.Stdin and returns the validated Config.
func LoadStdin() (*Config, error) {
	return Load(os.Stdin)
}

func finalize(k *koanf.Koanf) (*Config, error) {
	c := Defaults()
	if err := k.UnmarshalWithConf("", c, koanf.UnmarshalConf{Tag: "yaml"}); err != nil {
		return nil, errors.Wrap(err, "unmarshal")
	}

	if err := c.Validate(); err != nil {
		return nil, err
	}

	return c, nil
}
