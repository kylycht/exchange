package main

import (
	"database/sql"
	"fmt"
	"os"
	"os/signal"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/swagger"
	"github.com/kylycht/exchange/controller/converter"
	_ "github.com/kylycht/exchange/docs"
	"github.com/kylycht/exchange/service"
	"github.com/kylycht/exchange/service/forex"
	"github.com/kylycht/exchange/storage"
	"github.com/kylycht/exchange/storage/cache"
	"github.com/kylycht/exchange/storage/persistence"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

//	@title			C2F F2C Converter
//	@version		1.0
//	@description	Crypto-to-Fiat and Fiat-to-Crypto converter

// @host		localhost:3000
func main() {
	content, err := os.ReadFile("config.yaml")
	if err != nil {
		log.Error().Err(err).Msg("unable to read configuration file")
		os.Exit(1)
	}
	fmt.Println(string(content))
	cfg := Config{}
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		log.Error().Err(err).Msg("unable to read configuration file")
		os.Exit(1)
	}

	if err := New(cfg); err != nil {
		log.Error().Err(err).Msg("unable to initialize application")
		os.Exit(1)
	}
}

func New(cfg Config) error {
	a := Application{cfg: cfg}
	return a.init()
}

type Application struct {
	cfg            Config           // application configuration
	fiberApp       *fiber.App       // underlying fiber application
	db             storage.Storage  // persistence provider
	dbConn         *sql.DB          // underlying persistence connection
	cache          storage.Cache    // cache provider for rates
	exchangeClient service.Exchange // exchange rates provider
	stopC          chan os.Signal   // handle interrupt for clean up(close connections, etc)
}

func (a *Application) init() error {
	a.fiberApp = fiber.New()
	a.stopC = make(chan os.Signal)
	signal.Notify(a.stopC, os.Interrupt)

	connStr := fmt.Sprintf("postgresql://%s:%s@%s:%s/%s?sslmode=disable",
		a.cfg.DBUsername,
		a.cfg.DBPassword,
		a.cfg.DBHost,
		a.cfg.DBPort,
		a.cfg.DBName,
	)
	log.Debug().Str("connStr", connStr).Msg("initialize db connection")

	dbConn, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Error().Err(err).Msg("unable to connect to db")
		return err
	}

	a.dbConn = dbConn
	a.db = persistence.New(dbConn)

	exchangeClient, err := forex.New(a.cfg.ExchangeAPIKey)
	if err != nil {
		log.Error().Err(err).Msg("unable to create exchange client")
		return err
	}

	a.exchangeClient = exchangeClient
	mcache, err := cache.New(a.exchangeClient, a.db)
	if err != nil {
		log.Error().Err(err).Msg("unable to create cache")
		return err
	}

	a.cache = mcache
	a.buildRoutes()
	go a.stop()
	log.Debug().Msg("preparing fiber http server")

	if err := a.fiberApp.Listen(a.cfg.HTTPPort); err != nil {
		log.Error().Err(err).Msg("unable to start http server")
	}

	return nil
}

func (a *Application) buildRoutes() {
	a.fiberApp.Get("/swagger/*", swagger.HandlerDefault)
	a.fiberApp.Get("/convert", converter.New(a.cache).Convert)
}

func (a *Application) stop() {
	<-a.stopC
	a.fiberApp.Shutdown()
	a.dbConn.Close()
	os.Exit(0)
}
