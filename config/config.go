package config

import "os"

type Config struct {
	Port                string
	BaseURL             string
	DBPath              string
	MasterKey           string
	JWTSecret           string
	JWTExpireDays       string
	DefaultAIBaseURL    string
	DefaultAIKey        string
	DefaultAIModel      string
	DefaultAIDailyLimit string
}

func Load() Config {
	return Config{
		Port:                getenv("PORT", "8080"),
		BaseURL:             getenv("BASE_URL", "http://localhost:8080"),
		DBPath:              getenv("DB_PATH", "./recall.db"),
		MasterKey:           os.Getenv("MASTER_KEY"),
		JWTSecret:           os.Getenv("JWT_SECRET"),
		JWTExpireDays:       getenv("JWT_EXPIRE_DAYS", "30"),
		DefaultAIBaseURL:    getenv("DEFAULT_AI_BASE_URL", "https://api.deepseek.com/v1"),
		DefaultAIKey:        os.Getenv("DEFAULT_AI_KEY"),
		DefaultAIModel:      getenv("DEFAULT_AI_MODEL", "deepseek-chat"),
		DefaultAIDailyLimit: getenv("DEFAULT_AI_DAILY_LIMIT", "1"),
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
