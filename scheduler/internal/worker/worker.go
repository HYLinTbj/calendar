package worker

import (
	"context"
	"log"
	"time"

	"github.com/hylin/calendar/internal/repository"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Worker struct {
	repo *repository.RecurringEventRepository
}

func New(pool *pgxpool.Pool) *Worker {
	return &Worker{repo: repository.NewRecurringEventRepository(pool)}
}

func (w *Worker) Run(ctx context.Context) {
	log.Println("scheduler worker started, running every hour")
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	w.process(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.process(ctx)
		}
	}
}

func (w *Worker) process(ctx context.Context) {
	if err := w.repo.GeneratePending(ctx); err != nil {
		log.Printf("generate pending: %v", err)
	}
}
