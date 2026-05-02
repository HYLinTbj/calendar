package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/hylin/calendar/scheduler/internal/db"
	"github.com/hylin/calendar/scheduler/internal/worker"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := db.NewPool(ctx)
	if err != nil {
		log.Fatalf("connect to db: %v", err)
	}
	defer pool.Close()

	w := worker.New(pool)
	go w.Run(ctx)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("scheduler worker shutting down")
}
