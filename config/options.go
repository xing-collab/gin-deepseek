package config

import "os"

// BaseConfig contains settings shared by both API clients.
type BaseConfig struct {
	baseUrl   string
	apiKey    string
	modelName string
}

// Option customizes a client configuration.
type Option func(*BaseConfig)

// WithBaseURL overrides the default API endpoint.
func WithBaseURL(url string) Option {
	return func(c *BaseConfig) { c.baseUrl = url }
}

// WithAPIKey overrides the API key read from the environment.
func WithAPIKey(key string) Option {
	return func(c *BaseConfig) { c.apiKey = key }
}

// WithModel overrides the default model.
func WithModel(name string) Option {
	return func(c *BaseConfig) { c.modelName = name }
}

func newBaseConfig(baseURL, apiKeyEnv, model string, opts ...Option) *BaseConfig {
	cfg := &BaseConfig{
		baseUrl:   baseURL,
		apiKey:    os.Getenv(apiKeyEnv),
		modelName: model,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	return cfg
}
