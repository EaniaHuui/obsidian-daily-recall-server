package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"obsidian-recall/server/config"
	"obsidian-recall/server/crypto"
	"obsidian-recall/server/db"
	"obsidian-recall/server/handlers"
)

func main() {
	cfg := config.Load()

	store, err := db.Open(db.Config{Path: cfg.DBPath})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		_ = store.Close()
	}()

	secretBox, err := crypto.NewSecretBox(cfg.MasterKey)
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	deps := handlers.Dependencies{
		Config:    cfg,
		Store:     store,
		SecretBox: secretBox,
	}
	handlers.RegisterRoutes(mux, deps)
	go handlers.StartRecallWorker(context.Background(), deps)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("obsidian-recall server listening on :%s", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
