package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/hylin/calendar/internal/db"
	"github.com/hylin/calendar/notification/internal/mailer"
	"github.com/hylin/calendar/notification/internal/worker"
	"github.com/redis/go-redis/v9"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rdb := redis.NewClient(&redis.Options{
		Addr: getEnv("REDIS_ADDR", "localhost:6379"),
	})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("connect to redis: %v", err)
	}

	pool, err := db.NewPool(ctx)
	if err != nil {
		log.Fatalf("connect to db: %v", err)
	}
	defer pool.Close()

	baseURL := getEnv("BASE_URL", "http://localhost:8080")
	m := mailer.New()
	w := worker.New(rdb, pool, m, baseURL)
	go w.Run(ctx)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("notification worker shutting down")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
