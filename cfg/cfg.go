package cfg

import (
	"errors"
	"flag"
	"fmt"
	stdlog "log"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/aofei/air"
	"github.com/mitchellh/mapstructure"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	a = air.Default

	Zerolog struct {
		LoggerLevel string `mapstructure:"logger_level"`
	}

	Post struct {
		Root string `mapstructure:"root"`
	}
)

func init() {
	cf := flag.String("config", "config.toml", "configuration file")
	flag.Parse()

	m := map[string]interface{}{}
	if _, err := toml.DecodeFile(*cf, &m); err != nil {
		panic(fmt.Errorf(
			"failed to decode configuration file: %v",
			err,
		))
	}

	if err := mapstructure.Decode(m["air"], a); err != nil {
		panic(fmt.Errorf(
			"failed to decode air configuration items: %v",
			err,
		))
	}

	a.ErrorLogger = stdlog.New(&errorLogWriter{}, "", 0)

	if err := mapstructure.Decode(m["zerolog"], &Zerolog); err != nil {
		panic(fmt.Errorf(
			"failed to decode zerolog configuration items: %v",
			err,
		))
	}

	if err := mapstructure.Decode(m["post"], &Post); err != nil {
		panic(fmt.Errorf(
			"failed to decode post configuration items: %v",
			err,
		))
	}

	zerolog.TimeFieldFormat = ""
	switch Zerolog.LoggerLevel {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	case "fatal":
		zerolog.SetGlobalLevel(zerolog.FatalLevel)
	case "panic":
		zerolog.SetGlobalLevel(zerolog.PanicLevel)
	case "no":
		zerolog.SetGlobalLevel(zerolog.NoLevel)
	case "disabled":
		zerolog.SetGlobalLevel(zerolog.Disabled)
	}

	if a.DebugMode {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}
}

type errorLogWriter struct{}

func (elw *errorLogWriter) Write(b []byte) (int, error) {
	log.Error().Err(errors.New(strings.TrimSuffix(string(b), "\n"))).
		Str("app_name", a.AppName).
		Msg("air error")

	return len(b), nil
}
