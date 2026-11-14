package main

import "strings"

// Config is read once at startup from the environment.
type Config struct {
	Port             string
	DataFile         string
	APIKeys          []string // full access
	ReadOnlyKeys     []string // reads only; writes return 403
	EnableTestRoutes bool     // exposes POST /v1/__mock/reset
}

// LoadConfig builds a Config from getenv (os.Getenv in production).
func LoadConfig(getenv func(string) string) Config {
	port := strings.TrimSpace(getenv("PORT"))
	if port == "" {
		port = "8080"
	}
	dataFile := strings.TrimSpace(getenv("DATA_FILE"))
	if dataFile == "" {
		dataFile = "/data/state.json"
	}
	return Config{
		Port:             port,
		DataFile:         dataFile,
		APIKeys:          splitList(getenv("API_KEYS")),
		ReadOnlyKeys:     splitList(getenv("API_KEYS_READONLY")),
		EnableTestRoutes: parseBool(getenv("ENABLE_TEST_ROUTES")),
	}
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
