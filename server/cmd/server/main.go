package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/notallthere404/futurecast/server/pkg/httpx"
	"github.com/notallthere404/futurecast/server/pkg/inference"
	"github.com/notallthere404/futurecast/server/pkg/logger"
	"github.com/notallthere404/futurecast/server/pkg/registry"
	"github.com/notallthere404/futurecast/server/pkg/scheduler"
	"github.com/notallthere404/futurecast/server/pkg/schema"
	"github.com/notallthere404/futurecast/server/pkg/server"

	classificationcontroller "github.com/notallthere404/futurecast/server/pkg/controller/classification"
	configcontroller "github.com/notallthere404/futurecast/server/pkg/controller/config"
	inferencecontroller "github.com/notallthere404/futurecast/server/pkg/controller/inference"
	schedulercontroller "github.com/notallthere404/futurecast/server/pkg/controller/scheduler"
	sourcecontroller "github.com/notallthere404/futurecast/server/pkg/controller/source"
	systemcontroller "github.com/notallthere404/futurecast/server/pkg/controller/system"
	viewcontroller "github.com/notallthere404/futurecast/server/pkg/controller/view"

	articlestore "github.com/notallthere404/futurecast/server/pkg/registry/article"
	classificationstore "github.com/notallthere404/futurecast/server/pkg/registry/classification"
	monitorstore "github.com/notallthere404/futurecast/server/pkg/registry/monitor"
	sourcestore "github.com/notallthere404/futurecast/server/pkg/registry/source"
	viewstore "github.com/notallthere404/futurecast/server/pkg/registry/view"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	configCtrl, err := configcontroller.New(slog.Default(), os.Getenv("CONFIG_FILE"))
	if err != nil {
		slog.Error("config initialization failed", "error", err)
		os.Exit(1)
	}
	cfg := configCtrl.Get()
	log := logger.New("server", cfg.Server.LogLevel)
	dsn := cfg.Server.ExtDb
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	db, err := registry.New(log, dsn)
	if err != nil {
		log.Error("database initialization failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.EnsureBaseSchema(ctx); err != nil {
		log.Error("base schema bootstrap failed", "error", err)
		os.Exit(1)
	}

	sources := sourcestore.New(db)
	articles := articlestore.New(db)
	classifications := classificationstore.New(db)
	monitor := monitorstore.New(db)
	views := viewstore.New(db)
	infClient := inference.New(log)
	infContainer := inference.NewContainer(log, os.Getenv("COMPOSE_PROJECT_DIR"))
	cron := scheduler.New(log)
	httpClient := httpx.New(log)

	guard := schema.New()
	// Pick the inference mode from config. continuous = level-triggered
	// background loop pulling from the articles table; manual = sync
	// per-request via the classify route. Default to continuous when
	// the config field is unset.
	var infMode inference.Mode
	switch cfg.Inference.Mode {
	case "manual":
		infMode = inference.NewManualMode(log, classifications, guard)
	default:
		infMode = inference.NewContinuousMode(log, articles, classifications, guard)
	}
	inferenceCtrl := inferencecontroller.New(log, configCtrl, infClient, infContainer, infMode)
	inferenceCtrl.Start(ctx)
	sourceCtrl := sourcecontroller.New(log, sources, articles, httpClient, guard, inferenceCtrl)
	classificationCtrl := classificationcontroller.New(log, configCtrl, classifications, guard)
	viewCtrl := viewcontroller.New(log, configCtrl, views, classifications, articles, sources)
	schedulerCtrl := schedulercontroller.New(log, cron)
	systemCtrl := systemcontroller.New(log, configCtrl, sources, sourceCtrl, classifications, monitor, schedulerCtrl, guard)

	if err := systemCtrl.Startup(ctx); err != nil {
		log.Error("initialization failed", "error", err)
		os.Exit(1)
	}

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	api := server.New(log, logger.Tx, addr, configCtrl, systemCtrl, sourceCtrl, viewCtrl, classificationCtrl, inferenceCtrl, schedulerCtrl)
	if err := api.ListenAndServe(); err != nil {
		log.Error("could not start server", "error", err)
	}
}
