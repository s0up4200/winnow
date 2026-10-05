package winnow

import (
	"bytes"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// Config is the loaded configuration file.
type Config struct {
	Listen  string                `yaml:"listen"`
	Sources map[string]Source     `yaml:"sources"`
	Sinks   map[string]SinkConfig `yaml:"sinks"`
	Routes  []Route               `yaml:"routes"`
}

// Source is one webhook endpoint, /hook/<name>, with its own secret.
type Source struct {
	Secret string `yaml:"secret"`
}

// SinkConfig is the configuration of one Sink.
type SinkConfig struct {
	Discord string `yaml:"discord"`
}

// Route is one entry in the ordered route list.
type Route struct {
	Name  string   `yaml:"name"` // Load sets "#<position>" when the file has no name
	Match *Matcher `yaml:"match"`
	To    []string `yaml:"to"`
}

// Load parses a configuration file. Startup and `winnow check` both call it.
// An error stops startup. A warning does not.
func Load(data []byte) (cfg *Config, errs []error, warns []string) {
	cfg = &Config{Listen: ":8080"}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, []error{err}, nil
	}
	for name, s := range cfg.Sources {
		if s.Secret == "" {
			errs = append(errs, fmt.Errorf("sources.%s: secret is empty", name))
		}
	}
	for i := range cfg.Routes {
		r := &cfg.Routes[i]
		if r.Name == "" {
			r.Name = fmt.Sprintf("#%d", i+1)
		}
		if r.Match == nil {
			errs = append(errs, fmt.Errorf("route %s: match is missing", r.Name))
		}
		for _, to := range r.To {
			if _, ok := cfg.Sinks[to]; !ok {
				errs = append(errs, fmt.Errorf("route %s: sink %q is not in sinks", r.Name, to))
			}
		}
	}
	return cfg, errs, warns
}
