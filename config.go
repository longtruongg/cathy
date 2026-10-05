package main

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	AppPassword, ToMail, FromMail string
}

func loadConfig() (*Config, error) {
	_ = godotenv.Load()
	cfg := Config{
		AppPassword: os.Getenv("APP_PASSWORD"),
		ToMail:      os.Getenv("TO_MAIL"),
		FromMail:    os.Getenv("FROM_MAIL"),
	}
	if cfg.AppPassword == "" || cfg.ToMail == "" || cfg.FromMail == "" {
		return nil, fmt.Errorf("APP_PASSWORD, TO_MAIL, and FROM_MAIL must be set in .env (next to the executable) or the process environment")
	}
	return &cfg, nil
}
