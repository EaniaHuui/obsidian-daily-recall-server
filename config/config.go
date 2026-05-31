package config

import "os"

type Config struct {
	Port          string
	BaseURL       string
	DBPath        string
	MasterKey     string
	JWTSecret     string
	JWTExpireDays string
}

func Load() Config {
	return Config{
		Port:          getenv("PORT", "8080"),
		BaseURL:       getenv("BASE_URL", "http://localhost:8080"),
		DBPath:        getenv("DB_PATH", "./recall.db"),
		MasterKey:     os.Getenv("MASTER_KEY"),
		JWTSecret:     os.Getenv("JWT_SECRET"),
		JWTExpireDays: getenv("JWT_EXPIRE_DAYS", "30"),
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
