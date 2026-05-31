package handlers

import (
	"context"
	"log"
	"time"
)

func StartRecallWorker(ctx context.Context, deps Dependencies) {
	api := newAPI(deps)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			log.Printf("worker: tick")
			runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			api.processDueRecalls(runCtx)
			cancel()
		}
	}
}
