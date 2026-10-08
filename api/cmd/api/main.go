package main

import (
	"context"
	"log"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/hylin/calendar/internal/db"
	"github.com/hylin/calendar/internal/handler"
	"github.com/hylin/calendar/internal/middleware"
	"github.com/hylin/calendar/internal/queue"
	"github.com/hylin/calendar/internal/repository"
	"github.com/redis/go-redis/v9"
)

func main() {
	if err := middleware.CheckJWTSecret(); err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	pool, err := db.NewPool(ctx)
	if err != nil {
		log.Fatalf("connect to db: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr: getEnv("REDIS_ADDR", "localhost:6379"),
	})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("connect to redis: %v", err)
	}

	userRepo := repository.NewUserRepository(pool)
	calRepo := repository.NewCalendarRepository(pool)
	eventRepo := repository.NewEventRepository(pool)
	recurringRepo := repository.NewRecurringEventRepository(pool)
	inviteRepo := repository.NewInvitationRepository(pool)
	categoryRepo := repository.NewCategoryRepository(pool)
	categoryGroupRepo := repository.NewCategoryGroupRepository(pool)
	shareRepo := repository.NewCalendarShareRepository(pool)
	taskRepo := repository.NewTaskRepository(pool)
	traceRepo := repository.NewTraceRepository(pool)
	reminderQueue := queue.NewReminderQueue(rdb)

	if n, err := recurringRepo.RepairLegacyExceptions(ctx); err != nil {
		log.Printf("repair legacy recurring exceptions: %v", err)
	} else if n > 0 {
		log.Printf("repaired %d recurring-event instances edited before exceptions were tracked", n)
	}

	authHandler := handler.NewAuthHandler(userRepo, calRepo)
	userHandler := handler.NewUserHandler(userRepo)
	calHandler := handler.NewCalendarHandler(calRepo)
	shareHandler := handler.NewCalendarShareHandler(shareRepo, calRepo, userRepo)
	eventHandler := handler.NewEventHandler(eventRepo, calRepo, shareRepo, inviteRepo, recurringRepo, categoryRepo, reminderQueue)
	recurringHandler := handler.NewRecurringEventHandler(recurringRepo, calRepo, categoryRepo)
	inviteHandler := handler.NewInvitationHandler(inviteRepo)
	categoryHandler := handler.NewCategoryHandler(categoryRepo, categoryGroupRepo)
	categoryGroupHandler := handler.NewCategoryGroupHandler(categoryGroupRepo)
	taskHandler := handler.NewTaskHandler(taskRepo, categoryRepo)
	traceHandler := handler.NewTraceHandler(traceRepo, categoryRepo)
	icsHandler := handler.NewICSHandler(calRepo, eventRepo, recurringRepo, inviteRepo)
	freeBusyHandler := handler.NewFreeBusyHandler(eventRepo, userRepo)

	r := gin.Default()
	r.Use(middleware.CORS())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	auth := r.Group("/auth")
	{
		auth.POST("/register", authHandler.Register)
		auth.POST("/login", authHandler.Login)
	}

	protected := r.Group("/", middleware.Auth(userRepo))
	{
		users := protected.Group("/users")
		{
			users.GET("/me", userHandler.GetProfile)
			users.PUT("/me", userHandler.UpdateProfile)
			users.DELETE("/me", userHandler.DeleteAccount)
		}

		cals := protected.Group("/calendars")
		{
			cals.POST("", calHandler.Create)
			cals.GET("", calHandler.List)
			cals.GET("/shared-with-me", shareHandler.ListSharedWithMe)
			cals.GET("/:id", calHandler.GetByID)
			cals.PUT("/:id", calHandler.Update)
			cals.DELETE("/:id", calHandler.Delete)
			cals.GET("/:id/export", icsHandler.Export)
			cals.POST("/:id/import", icsHandler.Import)
			cals.POST("/:id/shares", shareHandler.Share)
			cals.GET("/:id/shares", shareHandler.ListShares)
			cals.DELETE("/:id/shares/:share_id", shareHandler.RemoveShare)
		}

		protected.GET("/free-busy", freeBusyHandler.Query)

		events := protected.Group("/events")
		{
			events.POST("", eventHandler.Create)
			events.GET("", eventHandler.List)
			events.GET("/search", eventHandler.Search)
			events.GET("/stats", eventHandler.Stats)
			events.GET("/stats/weekly", eventHandler.WeeklyStats)
			events.GET("/:id", eventHandler.GetByID)
			events.PUT("/:id", eventHandler.Update)
			events.PUT("/:id/recurrence", eventHandler.UpdateRecurrence)
			events.DELETE("/:id", eventHandler.Delete)
			events.DELETE("/:id/recurrence", eventHandler.DeleteRecurrence)
		}

		cats := protected.Group("/categories")
		{
			cats.POST("", categoryHandler.Create)
			cats.GET("", categoryHandler.List)
			cats.GET("/:id", categoryHandler.GetByID)
			cats.PUT("/:id", categoryHandler.Update)
			cats.DELETE("/:id", categoryHandler.Delete)
		}

		groups := protected.Group("/category-groups")
		{
			groups.POST("", categoryGroupHandler.Create)
			groups.GET("", categoryGroupHandler.List)
			groups.GET("/:id", categoryGroupHandler.GetByID)
			groups.PUT("/:id", categoryGroupHandler.Update)
			groups.DELETE("/:id", categoryGroupHandler.Delete)
		}

		recurring := protected.Group("/recurring-events")
		{
			recurring.POST("", recurringHandler.Create)
			recurring.GET("", recurringHandler.List)
			recurring.GET("/:id", recurringHandler.GetByID)
			recurring.PUT("/:id", recurringHandler.Update)
			recurring.DELETE("/:id", recurringHandler.Delete)
		}

		tasks := protected.Group("/tasks")
		{
			tasks.POST("", taskHandler.Create)
			tasks.GET("", taskHandler.List)
			tasks.GET("/:id", taskHandler.GetByID)
			tasks.PUT("/:id", taskHandler.Update)
			tasks.DELETE("/:id", taskHandler.Delete)
		}

		traces := protected.Group("/traces")
		{
			traces.POST("", traceHandler.Create)
			traces.GET("", traceHandler.List)
			traces.GET("/:id", traceHandler.GetByID)
			traces.PUT("/:id", traceHandler.Update)
			traces.DELETE("/:id", traceHandler.Delete)
		}
	}

	// Token-authenticated — no session required, the token is the credential.
	r.GET("/invitations/:token/accept", inviteHandler.Accept)
	r.GET("/invitations/:token/decline", inviteHandler.Decline)
	r.GET("/invitations/:token/tentative", inviteHandler.Tentative)

	port := getEnv("PORT", "8080")
	log.Printf("api listening on :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
