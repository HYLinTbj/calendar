//go:build integration

package handler_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/hylin/calendar/api/internal/db"
	"github.com/hylin/calendar/api/internal/handler"
	"github.com/hylin/calendar/api/internal/middleware"
	"github.com/hylin/calendar/api/internal/queue"
	"github.com/hylin/calendar/api/internal/repository"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	testPool   *pgxpool.Pool
	testRDB    *redis.Client
	testRouter *gin.Engine
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()

	pgContainer, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("calendar"),
		tcpostgres.WithUsername("calendar"),
		tcpostgres.WithPassword("calendar"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		panic("start postgres container: " + err.Error())
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic("get connection string: " + err.Error())
	}

	testPool, err = pgxpool.New(ctx, connStr)
	if err != nil {
		panic("connect to test db: " + err.Error())
	}

	if err := db.Migrate(ctx, testPool); err != nil {
		panic("migrate test db: " + err.Error())
	}

	mr, err := miniredis.Run()
	if err != nil {
		panic("start miniredis: " + err.Error())
	}
	testRDB = redis.NewClient(&redis.Options{Addr: mr.Addr()})

	testRouter = buildRouter(testPool, testRDB)

	code := m.Run()

	testPool.Close()
	testRDB.Close()
	mr.Close()
	_ = pgContainer.Terminate(ctx)
	os.Exit(code)
}

func buildRouter(pool *pgxpool.Pool, rdb *redis.Client) *gin.Engine {
	userRepo := repository.NewUserRepository(pool)
	calRepo := repository.NewCalendarRepository(pool)
	eventRepo := repository.NewEventRepository(pool)
	recurringRepo := repository.NewRecurringEventRepository(pool)
	inviteRepo := repository.NewInvitationRepository(pool)
	categoryRepo := repository.NewCategoryRepository(pool)
	shareRepo := repository.NewCalendarShareRepository(pool)
	reminderQueue := queue.NewReminderQueue(rdb)

	authHandler := handler.NewAuthHandler(userRepo, calRepo)
	userHandler := handler.NewUserHandler(userRepo)
	calHandler := handler.NewCalendarHandler(calRepo)
	shareHandler := handler.NewCalendarShareHandler(shareRepo, calRepo, userRepo)
	eventHandler := handler.NewEventHandler(eventRepo, calRepo, shareRepo, inviteRepo, recurringRepo, reminderQueue)
	recurringHandler := handler.NewRecurringEventHandler(recurringRepo, calRepo)
	inviteHandler := handler.NewInvitationHandler(inviteRepo)
	categoryHandler := handler.NewCategoryHandler(categoryRepo)
	icsHandler := handler.NewICSHandler(calRepo, eventRepo, recurringRepo, inviteRepo)
	freeBusyHandler := handler.NewFreeBusyHandler(eventRepo, userRepo)

	r := gin.New()
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	auth := r.Group("/auth")
	{
		auth.POST("/register", authHandler.Register)
		auth.POST("/login", authHandler.Login)
	}

	protected := r.Group("/", middleware.Auth())
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
			events.GET("/:id", eventHandler.GetByID)
			events.PUT("/:id", eventHandler.Update)
			events.PUT("/:id/recurrence", eventHandler.UpdateRecurrence)
			events.DELETE("/:id", eventHandler.Delete)
		}

		cats := protected.Group("/categories")
		{
			cats.POST("", categoryHandler.Create)
			cats.GET("", categoryHandler.List)
			cats.GET("/:id", categoryHandler.GetByID)
			cats.PUT("/:id", categoryHandler.Update)
			cats.DELETE("/:id", categoryHandler.Delete)
		}

		recurring := protected.Group("/recurring-events")
		{
			recurring.POST("", recurringHandler.Create)
			recurring.GET("", recurringHandler.List)
			recurring.GET("/:id", recurringHandler.GetByID)
			recurring.PUT("/:id", recurringHandler.Update)
			recurring.DELETE("/:id", recurringHandler.Delete)
		}
	}

	r.GET("/invitations/:token/accept", inviteHandler.Accept)
	r.GET("/invitations/:token/decline", inviteHandler.Decline)
	r.GET("/invitations/:token/tentative", inviteHandler.Tentative)

	return r
}
